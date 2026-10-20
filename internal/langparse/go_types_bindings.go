package langparse

import (
	"context"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/calleefilter"

	"golang.org/x/tools/go/callgraph/cha"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

func BindGoTypes(root string, symbols []Symbol, fileHashes map[string]string, calls map[string][]CallEdge) (map[string][]CallEdge, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	fset := token.NewFileSet()
	loaded, err := packages.Load(&packages.Config{
		Context: ctx, Dir: root, Fset: fset,
		BuildFlags: []string{"-mod=readonly"},
		Mode:       packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles | packages.NeedImports | packages.NeedTypes | packages.NeedSyntax | packages.NeedTypesInfo,
		Env:        append(os.Environ(), "GOPROXY=off", "GOSUMDB=off", "GONOPROXY=none", "GOVCS=*:off"),
	}, "./...")
	if err != nil {
		return calls, fmt.Errorf("Go compiler bindings: %w", err)
	}
	if ctx.Err() != nil {
		return calls, fmt.Errorf("Go compiler bindings: %w", ctx.Err())
	}
	type definitionKey struct {
		file, name string
		line       int
	}
	definitions := map[definitionKey]Symbol{}
	for _, symbol := range symbols {
		if symbol.Lang == "go" || strings.HasSuffix(symbol.File, ".go") {
			definitions[definitionKey{filepath.ToSlash(symbol.File), symbol.FullName(), symbol.Line}] = symbol
		}
	}
	objects := map[*types.Func]Symbol{}
	owners := map[*ast.FuncDecl]Symbol{}
	files := map[*ast.File]string{}
	replacements := map[string]map[string][]CallEdge{}
	dynamicSites := map[token.Pos]bool{}
	for _, pkg := range loaded {
		if pkg.IllTyped || pkg.TypesInfo == nil {
			continue
		}
		for _, file := range pkg.Syntax {
			rel, err := filepath.Rel(root, fset.Position(file.Pos()).Filename)
			if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				continue
			}
			rel = filepath.ToSlash(rel)
			if _, indexed := fileHashes[rel]; !indexed {
				continue
			}
			files[file] = rel
			for _, declaration := range file.Decls {
				if group, ok := declaration.(*ast.GenDecl); ok {
					for _, specification := range group.Specs {
						typeSpec, ok := specification.(*ast.TypeSpec)
						if !ok {
							continue
						}
						interfaceType, ok := typeSpec.Type.(*ast.InterfaceType)
						if !ok {
							continue
						}
						for _, field := range interfaceType.Methods.List {
							for _, name := range field.Names {
								key := definitionKey{rel, typeSpec.Name.Name + "." + name.Name, fset.Position(field.Pos()).Line}
								if symbol, found := definitions[key]; found {
									if object, ok := pkg.TypesInfo.Defs[name].(*types.Func); ok {
										objects[object.Origin()] = symbol
									}
								}
							}
						}
					}
				}
				function, ok := declaration.(*ast.FuncDecl)
				if !ok {
					continue
				}
				name := function.Name.Name
				if function.Recv != nil && len(function.Recv.List) > 0 {
					name = ReceiverName(function.Recv.List[0].Type) + "." + name
				}
				if symbol, found := definitions[definitionKey{rel, name, fset.Position(function.Pos()).Line}]; found {
					owners[function] = symbol
					if object, ok := pkg.TypesInfo.Defs[function.Name].(*types.Func); ok {
						objects[object.Origin()] = symbol
					}
				}
			}
		}
	}
	for _, pkg := range loaded {
		if pkg.IllTyped || pkg.TypesInfo == nil {
			continue
		}
		for _, file := range pkg.Syntax {
			rel, indexed := files[file]
			if !indexed {
				continue
			}
			retTypes := map[string]string{}
			for _, declaration := range file.Decls {
				if function, ok := declaration.(*ast.FuncDecl); ok && function.Recv == nil && function.Type.Results != nil && len(function.Type.Results.List) > 0 {
					retTypes[function.Name.Name] = GoTypeReference(function.Type.Results.List[0].Type)
				}
			}
			for _, declaration := range file.Decls {
				function, ok := declaration.(*ast.FuncDecl)
				if !ok || function.Body == nil {
					continue
				}
				owner, indexed := owners[function]
				if !indexed {
					continue
				}
				if replacements[owner.FullName()] == nil {
					replacements[owner.FullName()] = map[string][]CallEdge{}
				}
				edges := replacements[owner.FullName()][rel]
				WalkGoCalls(function, retTypes, func(call *ast.CallExpr, local LocalTypes) {
					raw := CalleeName(call.Fun)
					target := ResolveCallee(raw, local)
					if target == "" || target == owner.FullName() || calleefilter.IsNoiseCalleeFor(raw, calleefilter.ChainHasCall(call.Fun), rel) {
						return
					}
					object := goCallObject(call.Fun, pkg.TypesInfo)
					if _, conversion := object.(*types.TypeName); conversion {
						return
					}
					reference, localName := local[strings.SplitN(raw, ".", 2)[0]]
					edge := CallEdge{Target: target, SourceFile: rel, Confidence: ConfidenceHigh, Dynamic: localName && (reference == "" || !strings.Contains(raw, "."))}
					if target != raw {
						edge.Confidence = ConfidenceMedium
					}
					if object == nil && pkg.TypesInfo.Types[call.Fun].Type != nil {
						edge.FromID, edge.Binding, edge.Dynamic = owner.DeclarationID(), "go/types", true
						dynamicSites[call.Lparen] = true
					}
					if object != nil {
						edge.FromID, edge.Binding = owner.DeclarationID(), "go/types"
						if callee, ok := object.(*types.Func); ok {
							edge.Dynamic = false
							if symbol, found := objects[callee.Origin()]; found {
								edge.ToID = symbol.DeclarationID()
								edge.Confidence = ConfidenceHigh
							}
						} else {
							edge.Dynamic = true
							if _, functionValue := object.(*types.Var); functionValue {
								dynamicSites[call.Lparen] = true
							}
						}
					}
					edges = append(edges, edge)
				})
				replacements[owner.FullName()][rel] = edges
			}
		}
	}
	for owner, edges := range calls {
		var kept []CallEdge
		for position := range edges {
			if edges[position].Binding == "go/ssa-cha" || edges[position].Synth == "dispatch:go-cha" {
				continue
			}
			if edges[position].Binding == "go/types" {
				edges[position].FromID, edges[position].ToID, edges[position].Binding = "", "", ""
				edges[position].Confidence = ConfidenceMedium
			}
			kept = append(kept, edges[position])
		}
		calls[owner] = kept
	}
	for owner, byFile := range replacements {
		var merged []CallEdge
		for _, edge := range calls[owner] {
			if _, replaced := byFile[edge.SourceFile]; !replaced || edge.Synth != "" {
				merged = append(merged, edge)
			}
		}
		for _, edges := range byFile {
			merged = append(merged, edges...)
		}
		calls[owner] = DedupeCallEdges(merged)
	}
	addGoDispatchCandidates(calls, loaded, objects, owners, dynamicSites)
	return calls, nil
}

func addGoDispatchCandidates(calls map[string][]CallEdge, loaded []*packages.Package, objects map[*types.Func]Symbol, owners map[*ast.FuncDecl]Symbol, dynamicSites map[token.Pos]bool) {
	var healthy []*packages.Package
	for _, pkg := range loaded {
		if !pkg.IllTyped && pkg.TypesInfo != nil {
			healthy = append(healthy, pkg)
		}
	}
	if len(healthy) == 0 {
		return
	}
	positions := map[token.Pos]Symbol{}
	for declaration, symbol := range owners {
		positions[declaration.Name.Pos()] = symbol
		positions[declaration.Pos()] = symbol
	}
	definition := func(function *ssa.Function) (Symbol, bool) {
		if object, ok := function.Object().(*types.Func); ok {
			if symbol, found := objects[object.Origin()]; found {
				return symbol, true
			}
		}
		if function.Parent() == nil {
			if symbol, found := positions[function.Pos()]; found {
				return symbol, true
			}
		}
		if strings.Contains(function.Synthetic, "method wrapper") || strings.HasPrefix(function.Synthetic, "wrapper for ") {
			candidates := map[string]Symbol{}
			for _, block := range function.Blocks {
				for _, instruction := range block.Instrs {
					call, ok := instruction.(*ssa.Call)
					if !ok || call.Common().StaticCallee() == nil {
						continue
					}
					if object, ok := call.Common().StaticCallee().Object().(*types.Func); ok {
						if symbol, found := objects[object.Origin()]; found {
							candidates[symbol.DeclarationID()] = symbol
						}
					}
				}
			}
			if len(candidates) == 1 {
				for _, symbol := range candidates {
					return symbol, true
				}
			}
		}
		return Symbol{}, false
	}
	program, _ := ssautil.Packages(healthy, ssa.InstantiateGenerics)
	program.Build()
	graph := cha.CallGraph(program)
	for _, node := range graph.Nodes {
		for _, edge := range node.Out {
			if edge.Site == nil || (edge.Site.Common().StaticCallee() != nil && !dynamicSites[edge.Site.Common().Pos()]) {
				continue
			}
			callerFunction := edge.Caller.Func
			for callerFunction.Parent() != nil {
				callerFunction = callerFunction.Parent()
			}
			caller, hasCaller := definition(callerFunction)
			callee, hasCallee := definition(edge.Callee.Func)
			if !hasCaller || !hasCallee || caller.DeclarationID() == callee.DeclarationID() {
				continue
			}
			calls[caller.FullName()] = append(calls[caller.FullName()], CallEdge{
				Target: callee.FullName(), SourceFile: caller.File,
				FromID: caller.DeclarationID(), ToID: callee.DeclarationID(), Binding: "go/ssa-cha",
				Confidence: ConfidenceLow, Synth: "dispatch:go-cha",
			})
		}
	}
	for owner, edges := range calls {
		calls[owner] = DedupeCallEdges(edges)
	}
}

func goCallObject(expression ast.Expr, info *types.Info) types.Object {
	switch expression := expression.(type) {
	case *ast.Ident:
		return info.Uses[expression]
	case *ast.SelectorExpr:
		if selection := info.Selections[expression]; selection != nil {
			return selection.Obj()
		}
		return info.Uses[expression.Sel]
	case *ast.IndexExpr:
		return goCallObject(expression.X, info)
	case *ast.IndexListExpr:
		return goCallObject(expression.X, info)
	case *ast.ParenExpr:
		return goCallObject(expression.X, info)
	}
	return nil
}
