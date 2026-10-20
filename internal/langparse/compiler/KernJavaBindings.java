import com.sun.source.tree.*;
import com.sun.source.util.*;
import java.nio.file.*;
import java.util.*;
import javax.lang.model.element.*;
import javax.lang.model.type.*;
import javax.lang.model.util.*;
import javax.tools.*;

class KernJavaBindings {
    record Declaration(ExecutableElement element, String key, String file, long line,
                       String name, String receiver, String outer, List<String> params) {
        Map<String, Object> fields() {
            Map<String, Object> result = new LinkedHashMap<>();
            result.put("kind", "decl"); result.put("key", key); result.put("file", file);
            result.put("line", line); result.put("name", name); result.put("receiver", receiver);
            result.put("outer", outer); result.put("params", params);
            return result;
        }
        String fullName() { return receiver + "." + name; }
    }

    static String json(Object value) {
        if (value instanceof Map<?, ?> fields) {
            List<String> parts = new ArrayList<>();
            fields.forEach((key, item) -> parts.add(json(key.toString()) + ":" + json(item)));
            return "{" + String.join(",", parts) + "}";
        }
        if (value instanceof List<?> items) {
            return "[" + String.join(",", items.stream().map(KernJavaBindings::json).toList()) + "]";
        }
        if (value instanceof Number) return value.toString();
        StringBuilder result = new StringBuilder("\"");
        for (char character : value.toString().toCharArray()) {
            if (character == '"' || character == '\\') result.append('\\').append(character);
            else if (character < 32) result.append(String.format("\\u%04x", (int) character));
            else result.append(character);
        }
        return result.append('"').toString();
    }

    public static void main(String[] arguments) throws Exception {
        JavaCompiler compiler = ToolProvider.getSystemJavaCompiler();
        if (compiler == null) throw new IllegalStateException("a JDK with javac is required");
        DiagnosticCollector<JavaFileObject> diagnostics = new DiagnosticCollector<>();
        try (StandardJavaFileManager manager = compiler.getStandardFileManager(diagnostics, Locale.ROOT, null)) {
            List<String> paths = Files.readAllLines(Path.of(arguments[0]));
            List<String> options = List.of("-proc:none", "-implicit:none", "-classpath", arguments[1]);
            JavacTask task = (JavacTask) compiler.getTask(null, manager, diagnostics, options, null,
                manager.getJavaFileObjectsFromStrings(paths));
            List<CompilationUnitTree> units = new ArrayList<>();
            task.parse().forEach(units::add);
            task.analyze();
            List<Diagnostic<? extends JavaFileObject>> errors = diagnostics.getDiagnostics().stream()
                .filter(diagnostic -> diagnostic.getKind() == Diagnostic.Kind.ERROR).toList();
            if (!errors.isEmpty()) {
                errors.stream().limit(10).forEach(error -> System.err.println(error.getMessage(Locale.ROOT)));
                System.exit(2);
            }
            Trees trees = Trees.instance(task);
            SourcePositions positions = trees.getSourcePositions();
            Elements elements = task.getElements();
            Types types = task.getTypes();
            Map<ExecutableElement, Declaration> declarations = new LinkedHashMap<>();
            for (CompilationUnitTree unit : units) {
                String file = Path.of(unit.getSourceFile().toUri()).toAbsolutePath().normalize().toString();
                String source = unit.getSourceFile().getCharContent(true).toString();
                new TreePathScanner<Void, Void>() {
                    @Override public Void visitMethod(MethodTree method, Void ignored) {
                        long start = positions.getStartPosition(unit, method);
                        Element object = trees.getElement(getCurrentPath());
                        if (start >= 0 && object instanceof ExecutableElement executable &&
                            executable.getEnclosingElement() instanceof TypeElement receiver) {
                            String name = executable.getKind() == ElementKind.CONSTRUCTOR
                                ? receiver.getSimpleName().toString() : executable.getSimpleName().toString();
                            String outer = receiver.getEnclosingElement() instanceof TypeElement parent
                                ? parent.getSimpleName().toString() : "";
                            List<String> params = new ArrayList<>();
                            for (VariableTree parameter : method.getParameters()) {
                                long begin = positions.getStartPosition(unit, parameter);
                                long end = positions.getEndPosition(unit, parameter);
                                if (begin >= 0 && end >= begin) params.add(source.substring((int) begin, (int) end));
                            }
                            Declaration declaration = new Declaration(executable, file + "@" + start, file,
                                unit.getLineMap().getLineNumber(start), name, receiver.getSimpleName().toString(), outer, params);
                            declarations.put(executable, declaration);
                        }
                        return super.visitMethod(method, ignored);
                    }
                }.scan(unit, null);
            }
            declarations.values().forEach(declaration -> System.out.println(json(declaration.fields())));
            for (CompilationUnitTree unit : units) {
                new TreePathScanner<Void, Declaration>() {
                    @Override public Void visitMethod(MethodTree method, Declaration owner) {
                        return super.visitMethod(method, declarations.get(trees.getElement(getCurrentPath())));
                    }
                    void emit(Declaration owner, ExecutableElement target, Declaration callee, String synth) {
                        if (owner == null) return;
                        String name = target.getEnclosingElement() + "." + target;
                        System.out.println(json(Map.of("kind", "call", "caller", owner.key(),
                            "callee", callee == null ? "" : callee.key(),
                            "target", callee == null ? name : callee.fullName(), "synth", synth)));
                    }
                    @Override public Void visitMethodInvocation(MethodInvocationTree call, Declaration owner) {
                        Element object = trees.getElement(getCurrentPath());
                        if (object instanceof ExecutableElement target && owner != null) {
                            emit(owner, target, declarations.get(target), "");
                            Set<Modifier> modifiers = target.getModifiers();
                            TypeMirror receiver = owner.element().getEnclosingElement().asType();
                            if (call.getMethodSelect() instanceof MemberSelectTree select) {
                                TreePath selectPath = new TreePath(getCurrentPath(), select);
                                receiver = trees.getTypeMirror(new TreePath(selectPath, select.getExpression()));
                            }
                            boolean exactReceiver = call.getMethodSelect() instanceof MemberSelectTree select &&
                                (select.getExpression() instanceof NewClassTree || select.getExpression().toString().equals("super") ||
                                 select.getExpression().toString().endsWith(".super"));
                            if (!exactReceiver && receiver != null && receiver.getKind() != TypeKind.ERROR &&
                                !modifiers.contains(Modifier.STATIC) && !modifiers.contains(Modifier.PRIVATE) &&
                                !modifiers.contains(Modifier.FINAL)) {
                                for (Declaration candidate : declarations.values()) {
                                    TypeElement subtype = (TypeElement) candidate.element().getEnclosingElement();
                                    if (subtype.getKind().isClass() && !subtype.getModifiers().contains(Modifier.ABSTRACT) &&
                                        elements.overrides(candidate.element(), target, subtype) &&
                                        types.isSubtype(types.erasure(subtype.asType()), types.erasure(receiver))) {
                                        emit(owner, target, candidate, "dispatch:javac");
                                    }
                                }
                            }
                        }
                        return super.visitMethodInvocation(call, owner);
                    }
                    @Override public Void visitNewClass(NewClassTree call, Declaration owner) {
                        Element object = trees.getElement(getCurrentPath());
                        if (object instanceof ExecutableElement target) emit(owner, target, declarations.get(target), "");
                        return super.visitNewClass(call, owner);
                    }
                }.scan(unit, null);
            }
        }
    }
}
