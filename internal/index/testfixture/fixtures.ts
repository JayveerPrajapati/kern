// Parity fixture: TypeScript.
// Both the regex extractor (extractForeignRegex) and tree-sitter (tsExtract)
// must agree on symbol sets, call edges and confidences for this file.

function helper(x: number): number {
  return x + 1;
}

class Greeter {
  name: string;

  constructor(name: string) {
    this.name = name;
  }

  greet(): string {
    return "hello " + this.name;
  }

  loud(): string {
    return this.greet().toUpperCase();
  }
}

function makeGreeter(name: string): string {
  const g = new Greeter(name);
  return g.loud();
}

interface Named {
  name: string;
}

export function run(): void {
  const h = helper(2);
  const g = new Greeter("world");
  console.log(h, g.loud());
}

// Inherited-method call shape (Phase 5 / A5): shout() calls this.greet(),
// which is defined on the BASE class Greeter, not on LoudGreeter. The
// receiver-less callee edge (LoudGreeter.shout -> greet) exercises the
// intel layer's bare-callee resolution for an inherited receiver method.
class LoudGreeter extends Greeter {
  shout(): string {
    return this.greet() + "!";
  }
}