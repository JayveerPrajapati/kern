package langparse

import (
	"go/ast"
	"go/token"
)

func GoTypeReference(expression ast.Expr) string {
	switch typed := expression.(type) {
	case *ast.StarExpr:
		return GoTypeReference(typed.X)
	case *ast.IndexExpr:
		return GoTypeReference(typed.X)
	case *ast.IndexListExpr:
		return GoTypeReference(typed.X)
	case *ast.ParenExpr:
		return GoTypeReference(typed.X)
	case *ast.SelectorExpr:
		return CalleeName(typed)
	default:
		return ReceiverName(expression)
	}
}

type goCallVisitor struct {
	types    LocalTypes
	declared map[string]bool
	returns  map[string]string
	call     func(*ast.CallExpr, LocalTypes)
}

func WalkGoCalls(function *ast.FuncDecl, returns map[string]string, call func(*ast.CallExpr, LocalTypes)) {
	header := *function
	header.Body = nil
	visitor := &goCallVisitor{types: CollectLocalTypes(&header, returns), declared: map[string]bool{}, returns: returns, call: call}
	if function.Type.Results != nil {
		for _, field := range function.Type.Results.List {
			visitor.types.AddTypeField(field.Names, field.Type)
		}
	}
	for name := range visitor.types {
		visitor.declared[name] = true
	}
	visitor.walkBody(function.Body)
}

func (visitor *goCallVisitor) walkBody(body *ast.BlockStmt) {
	for _, statement := range body.List {
		ast.Walk(visitor, statement)
	}
}

func (visitor *goCallVisitor) child() *goCallVisitor {
	types := LocalTypes{}
	for name, reference := range visitor.types {
		types[name] = reference
	}
	return &goCallVisitor{types: types, declared: map[string]bool{}, returns: visitor.returns, call: visitor.call}
}

func (visitor *goCallVisitor) Visit(node ast.Node) ast.Visitor {
	if node == nil {
		return nil
	}
	switch typed := node.(type) {
	case *ast.BlockStmt:
		child := visitor.child()
		child.walkBody(typed)
		return nil
	case *ast.IfStmt:
		child := visitor.child()
		ast.Walk(child, typed.Init)
		ast.Walk(child, typed.Cond)
		ast.Walk(child, typed.Body)
		ast.Walk(child, typed.Else)
		return nil
	case *ast.ForStmt:
		child := visitor.child()
		ast.Walk(child, typed.Init)
		ast.Walk(child, typed.Cond)
		ast.Walk(child, typed.Body)
		ast.Walk(child, typed.Post)
		return nil
	case *ast.SwitchStmt:
		child := visitor.child()
		ast.Walk(child, typed.Init)
		ast.Walk(child, typed.Tag)
		ast.Walk(child, typed.Body)
		return nil
	case *ast.TypeSwitchStmt:
		child := visitor.child()
		ast.Walk(child, typed.Init)
		ast.Walk(child, typed.Assign)
		ast.Walk(child, typed.Body)
		return nil
	case *ast.RangeStmt:
		child := visitor.child()
		ast.Walk(child, typed.X)
		if typed.Tok == token.DEFINE {
			for _, expression := range []ast.Expr{typed.Key, typed.Value} {
				if identifier, ok := expression.(*ast.Ident); ok {
					child.types[identifier.Name] = ""
				}
			}
		}
		ast.Walk(child, typed.Body)
		return nil
	case *ast.CaseClause:
		child := visitor.child()
		for _, expression := range typed.List {
			ast.Walk(child, expression)
		}
		for _, statement := range typed.Body {
			ast.Walk(child, statement)
		}
		return nil
	case *ast.CommClause:
		child := visitor.child()
		ast.Walk(child, typed.Comm)
		for _, statement := range typed.Body {
			ast.Walk(child, statement)
		}
		return nil
	case *ast.FuncLit:
		child := visitor.child()
		if typed.Type.Params != nil {
			for _, field := range typed.Type.Params.List {
				for _, name := range field.Names {
					child.types[name.Name] = ""
					child.declared[name.Name] = true
				}
				child.types.AddTypeField(field.Names, field.Type)
			}
		}
		if typed.Type.Results != nil {
			for _, field := range typed.Type.Results.List {
				child.types.AddTypeField(field.Names, field.Type)
				for _, name := range field.Names {
					child.declared[name.Name] = true
				}
			}
		}
		child.walkBody(typed.Body)
		return nil
	case *ast.AssignStmt:
		for _, expression := range typed.Lhs {
			ast.Walk(visitor, expression)
		}
		for _, expression := range typed.Rhs {
			ast.Walk(visitor, expression)
		}
		for position, expression := range typed.Lhs {
			identifier, ok := expression.(*ast.Ident)
			if !ok || identifier.Name == "_" {
				continue
			}
			if typed.Tok == token.DEFINE && !visitor.declared[identifier.Name] {
				visitor.types[identifier.Name] = ""
				visitor.declared[identifier.Name] = true
			}
			if position < len(typed.Rhs) && visitor.types[identifier.Name] == "" {
				if reference := TypeNameOfExpr(typed.Rhs[position], visitor.returns); reference != "" {
					visitor.types[identifier.Name] = reference
				}
			}
		}
		return nil
	case *ast.ValueSpec:
		for _, expression := range typed.Values {
			ast.Walk(visitor, expression)
		}
		for position, name := range typed.Names {
			visitor.types[name.Name] = ""
			visitor.declared[name.Name] = true
			if typed.Type != nil {
				visitor.types.AddTypeField([]*ast.Ident{name}, typed.Type)
			} else if position < len(typed.Values) {
				visitor.types[name.Name] = TypeNameOfExpr(typed.Values[position], visitor.returns)
			}
		}
		return nil
	case *ast.CallExpr:
		visitor.call(typed, visitor.types)
	}
	return visitor
}
