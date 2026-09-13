// Parity fixture: Java.
// Both the regex extractor (extractForeignRegex) and tree-sitter (tsExtract)
// must agree on symbol sets, call edges and confidences for this file.

public class Fixtures {
    static int helper(int x) {
        return x + 1;
    }

    public static void main(String[] args) {
        Greeter g = new Greeter("world");
        System.out.println(g.loud());
        int h = helper(2);
        System.out.println(h);
    }
}

class Greeter {
    private String name;

    Greeter(String name) {
        this.name = name;
    }

    String greet() {
        return "hello " + this.name;
    }

    String loud() {
        return this.greet().toUpperCase();
    }
}