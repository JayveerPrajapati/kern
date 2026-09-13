# Parity fixture: Python.
# Both the regex extractor (extractForeignRegex) and tree-sitter (tsExtract)
# must agree on symbol sets, call edges and confidences for this file.

def helper(x):
    return x + 1


class Greeter:
    def __init__(self, name):
        self.name = name

    def greet(self):
        return "hello " + self.name

    def loud(self):
        return self.greet().upper()


def make_greeter(name):
    g = Greeter(name)
    return g.loud()


class Child(Greeter):
    def greet(self):
        return "hi " + self.name


def top_level():
    h = helper(2)
    c = Child("kid")
    return h + len(c.greet())