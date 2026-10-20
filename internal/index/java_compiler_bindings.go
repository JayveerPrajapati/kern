package index

import (
	"github.com/JayveerPrajapati/kern/internal/langparse"
)

// bindJavaCompiler runs the Java compiler-binding pass (javac fact
// extraction) via the langparse free function and folds the resulting
// call-edge updates back into the index.
func (ix *Index) bindJavaCompiler(classpath string) error {
	calls, err := langparse.BindJavaCompiler(ix.Root, ix.Symbols, classpath, ix.Calls)
	if err != nil {
		return err
	}
	ix.Calls = calls
	ix.JavaCompiler, ix.JavaClasspath = true, classpath
	return nil
}
