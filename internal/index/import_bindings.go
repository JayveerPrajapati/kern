package index

import (
	"path/filepath"
	"strings"
)

func importedModuleFile(source, target, module string) bool {
	if strings.HasSuffix(source, ".py") {
		if strings.HasPrefix(module, ".") {
			levels := len(module) - len(strings.TrimLeft(module, "."))
			directory := filepath.Dir(source)
			for level := 1; level < levels; level++ {
				directory = filepath.Dir(directory)
			}
			module = filepath.Join(directory, strings.ReplaceAll(strings.TrimLeft(module, "."), ".", "/"))
		} else {
			module = strings.ReplaceAll(module, ".", "/")
		}
		return target == module+".py" || target == filepath.Join(module, "__init__.py")
	}
	if !strings.HasPrefix(module, ".") {
		return false
	}
	module = filepath.Clean(filepath.Join(filepath.Dir(source), module))
	base := strings.TrimSuffix(target, filepath.Ext(target))
	return target == module || base == module || base == filepath.Join(module, "index")
}

func sameBindingLanguage(source, target string) bool {
	return source == target || ((source == "typescript" || source == "javascript") && (target == "typescript" || target == "javascript"))
}
