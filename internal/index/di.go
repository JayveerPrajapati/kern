package index

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	// Java Spring @Bean method declaration
	reSpringBeanMethod = regexp.MustCompile(`(?m)@Bean(?:\s*\([^)]*\))?\s*(?:\n\s*@[^\n]+)*\s*\n\s*(?:public|protected|private)?\s*(?:(?:final|synchronized|static)\s+)*([A-Za-z0-9_<>,\s\[\]]+)\s+([A-Za-z0-9_]+)\s*\(`)

	// Java Spring @Autowired / @Inject / @Resource field injection
	reSpringAutowiredField = regexp.MustCompile(`(?m)@(Autowired|Inject|Resource)(?:\s*\([^)]*\))?\s*(?:\n\s*@[^\n]+)*\s*\n\s*(?:private|protected|public)?\s*(?:final\s+)?([A-Za-z0-9_<>,\s\[\]]+)\s+([A-Za-z0-9_]+)\s*;`)

	// Java KafkaListener containerFactory reference
	reKafkaContainerFactory = regexp.MustCompile(`containerFactory\s*=\s*["']([^"']+)["']`)

	// Python FastAPI Depends(fn)
	rePyDepends = regexp.MustCompile(`Depends\s*\(\s*([a-zA-Z0-9_]+)\s*\)`)

	// NestJS TS constructor parameter injection
	reNestConstructorParam = regexp.MustCompile(`(?:private|protected|public|readonly)\s+([a-zA-Z0-9_]+)\s*:\s*([a-zA-Z0-9_]+)`)
)

type beanProvider struct {
	configClass string // enclosing configuration class
	fullName    string // method or class full name
	methodName  string // bean method name or empty
	returnType  string // declared return type simple name
}

// addFrameworkDIEdges bridges framework-level dependency injection (Spring Boot,
// NestJS, FastAPI) into the primary index call graph (ix.Calls) before
// computeCallers runs, so framework-injected dependencies are recognized as
// callers and in blast radius calculations.
func (ix *Index) addFrameworkDIEdges() {
	if ix.Calls == nil {
		ix.Calls = make(map[string][]CallEdge)
	}

	// 1. Collect bean definitions and providers
	beansByType := make(map[string][]beanProvider)
	beansByName := make(map[string][]beanProvider)

	// Register declared symbols (classes, methods, interfaces)
	for _, s := range ix.Symbols {
		if s.Kind == "class" || s.Kind == "interface" || s.Kind == "struct" {
			bp := beanProvider{
				configClass: s.Name,
				fullName:    s.FullName(),
				methodName:  "",
				returnType:  s.Name,
			}
			beansByType[s.Name] = append(beansByType[s.Name], bp)
			beansByName[strings.ToLower(s.Name)] = append(beansByName[strings.ToLower(s.Name)], bp)
		}
	}

	// Register inheritance implementations (e.g. JobServiceImpl implements IJobService)
	for base, subtypes := range ix.InheritedBy {
		for _, sub := range subtypes {
			bp := beanProvider{
				configClass: sub,
				fullName:    sub,
				methodName:  "",
				returnType:  base,
			}
			beansByType[base] = append(beansByType[base], bp)
		}
	}

	// Group symbols by file for targeted source parsing
	filesProcessed := make(map[string]bool)
	for _, s := range ix.Symbols {
		if s.File == "" || filesProcessed[s.File] {
			continue
		}
		filesProcessed[s.File] = true

		ext := strings.ToLower(filepath.Ext(s.File))
		if ext != ".java" && ext != ".ts" && ext != ".py" {
			continue
		}

		filePath := s.File
		if ix.Root != "" && !filepath.IsAbs(filePath) {
			filePath = filepath.Join(ix.Root, filePath)
		}

		srcBytes, err := os.ReadFile(filePath)
		if err != nil {
			continue
		}
		src := string(srcBytes)

		// Java: extract @Configuration and @Bean methods
		if ext == ".java" {
			classNames := extractJavaClasses(src)
			for _, cls := range classNames {
				beanMatches := reSpringBeanMethod.FindAllStringSubmatch(src, -1)
				for _, bm := range beanMatches {
					if len(bm) >= 3 {
						rawType := bm[1]
						methodName := bm[2]
						retType := javaSimpleTypeName(rawType)
						fullMethod := cls + "." + methodName

						bp := beanProvider{
							configClass: cls,
							fullName:    fullMethod,
							methodName:  methodName,
							returnType:  retType,
						}
						beansByType[retType] = append(beansByType[retType], bp)
						beansByName[methodName] = append(beansByName[methodName], bp)

						// Link configuration class to its own bean method
						ix.Calls[cls] = append(ix.Calls[cls], CallEdge{Target: fullMethod, Confidence: ConfidenceMedium})
					}
				}
			}
		}
	}

	// 2. Scan consumer files for injections and wire call edges
	for file := range filesProcessed {
		ext := strings.ToLower(filepath.Ext(file))
		filePath := file
		if ix.Root != "" && !filepath.IsAbs(filePath) {
			filePath = filepath.Join(ix.Root, filePath)
		}

		srcBytes, err := os.ReadFile(filePath)
		if err != nil {
			continue
		}
		src := string(srcBytes)

		if ext == ".java" {
			classes := extractJavaClasses(src)
			if len(classes) == 0 {
				continue
			}
			consumerClass := classes[0]

			// @Autowired / @Inject / @Resource fields
			injectedFields := reSpringAutowiredField.FindAllStringSubmatch(src, -1)
			for _, ifield := range injectedFields {
				if len(ifield) >= 4 {
					rawType := ifield[2]
					fieldName := ifield[3]
					typeName := javaSimpleTypeName(rawType)

					// Match providers by type
					if providers, ok := beansByType[typeName]; ok {
						for _, p := range providers {
							if p.fullName != "" && p.fullName != consumerClass {
								ix.Calls[consumerClass] = append(ix.Calls[consumerClass], CallEdge{Target: p.fullName, Confidence: ConfidenceMedium})
							}
							if p.configClass != "" && p.configClass != consumerClass && p.configClass != p.fullName {
								ix.Calls[consumerClass] = append(ix.Calls[consumerClass], CallEdge{Target: p.configClass, Confidence: ConfidenceMedium})
							}
						}
					}

					// Match providers by field name
					if providers, ok := beansByName[fieldName]; ok {
						for _, p := range providers {
							if p.fullName != "" && p.fullName != consumerClass {
								ix.Calls[consumerClass] = append(ix.Calls[consumerClass], CallEdge{Target: p.fullName, Confidence: ConfidenceMedium})
							}
							if p.configClass != "" && p.configClass != consumerClass && p.configClass != p.fullName {
								ix.Calls[consumerClass] = append(ix.Calls[consumerClass], CallEdge{Target: p.configClass, Confidence: ConfidenceMedium})
							}
						}
					}
				}
			}

			// Kafka containerFactory references
			kfMatches := reKafkaContainerFactory.FindAllStringSubmatch(src, -1)
			for _, km := range kfMatches {
				if len(km) >= 2 {
					factoryName := km[1]
					if providers, ok := beansByName[factoryName]; ok {
						for _, p := range providers {
							if p.fullName != "" && p.fullName != consumerClass {
								ix.Calls[consumerClass] = append(ix.Calls[consumerClass], CallEdge{Target: p.fullName, Confidence: ConfidenceMedium})
							}
							if p.configClass != "" && p.configClass != consumerClass && p.configClass != p.fullName {
								ix.Calls[consumerClass] = append(ix.Calls[consumerClass], CallEdge{Target: p.configClass, Confidence: ConfidenceMedium})
							}
						}
					}
				}
			}
		} else if ext == ".ts" {
			// NestJS constructor parameter injection
			nestMatches := reNestConstructorParam.FindAllStringSubmatch(src, -1)
			if len(nestMatches) > 0 {
				classes := extractTSClasses(src)
				if len(classes) > 0 {
					consumerClass := classes[0]
					for _, nm := range nestMatches {
						if len(nm) >= 3 {
							depType := nm[2]
							if depType != "" && depType != consumerClass {
								ix.Calls[consumerClass] = append(ix.Calls[consumerClass], CallEdge{Target: depType, Confidence: ConfidenceMedium})
							}
						}
					}
				}
			}
		} else if ext == ".py" {
			// Python FastAPI Depends(fn)
			depMatches := rePyDepends.FindAllStringSubmatch(src, -1)
			for _, dm := range depMatches {
				if len(dm) >= 2 {
					depFn := dm[1]
					// Find enclosing function/caller in this file
					for _, s := range ix.Symbols {
						if s.File == file && (s.Kind == "func" || s.Kind == "method") {
							ix.Calls[s.FullName()] = append(ix.Calls[s.FullName()], CallEdge{Target: depFn, Confidence: ConfidenceMedium})
						}
					}
				}
			}
		}
	}

	// Dedupe calls across the whole index
	for k := range ix.Calls {
		ix.Calls[k] = dedupeCallEdges(ix.Calls[k])
	}
}

var reJavaClassDecl = regexp.MustCompile(`(?m)(?:public|protected|private)?\s*(?:(?:final|abstract|static)\s+)*(?:class|interface|enum|record)\s+([A-Za-z0-9_]+)`)
var reTSClassDecl = regexp.MustCompile(`(?m)(?:export\s+)?(?:default\s+)?class\s+([A-Za-z0-9_]+)`)

func extractJavaClasses(src string) []string {
	matches := reJavaClassDecl.FindAllStringSubmatch(src, -1)
	var out []string
	for _, m := range matches {
		if len(m) >= 2 {
			out = append(out, m[1])
		}
	}
	return out
}

func extractTSClasses(src string) []string {
	matches := reTSClassDecl.FindAllStringSubmatch(src, -1)
	var out []string
	for _, m := range matches {
		if len(m) >= 2 {
			out = append(out, m[1])
		}
	}
	return out
}
