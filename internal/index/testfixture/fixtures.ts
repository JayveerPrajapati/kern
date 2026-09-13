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