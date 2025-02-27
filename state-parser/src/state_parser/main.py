import argparse
import ast
import pathlib

import dill


class GlobalScopeParser(ast.NodeVisitor):
    def __init__(self):
        self.global_funcs = {}
        self.global_vars = {}
        self.context = [("global", ())]

    def visit_FunctionDef(self, node: ast.FunctionDef) -> None:
        if self.context[-1][0] == "global":
            self.global_funcs[node.name] = node

        self.context.append(("function", ()))
        self.generic_visit(node)
        self.context.pop()

    def visit_Call(self, node: ast.Call) -> None:
        self.context.append(("call", ()))
        self.generic_visit(node)
        self.context.pop()

    def visit_Name(self, node: ast.Name) -> None:
        ctx, g = self.context[-1]
        if ctx == "global" or node.id in g:
            self.global_vars[node.id] = node


def is_valid_file(parser: argparse.ArgumentParser, arg):
    if not pathlib.Path(arg).is_file():
        parser.error(f"The file {arg} does not exist")
    elif pathlib.Path(arg).suffix != ".py":
        parser.error("Input file should be a Python file")

    return open(arg, "r")


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument(
        "-i",
        dest="file",
        help="input file to be parsed and evaluated",
        required=True,
        type=lambda x: is_valid_file(parser, x),
    )
    parser.add_argument("-o", dest="output", help="output filename", required=True)
    parser.add_argument(
        "--state-files",
        dest="state_files",
        nargs="*",
        help="node state files to deserialize",
    )
    args = parser.parse_args()

    ast_parsed = ast.parse(args.file.read())
    args.file.close()

    visitor = GlobalScopeParser()
    visitor.visit(ast_parsed)

    # Currently keep track of both global namespace and filtered state since
    # not sure if I want to serialize the entire execution state like __builtins__ yet.
    # This could change in the future and would deprecate the global parser
    namespace = {}
    state = {}

    for pickled_state in args.state_files:
        with open(pickled_state, "rb") as f:
            prev_state = dill.load(f)
        namespace.update(prev_state)
        state.update(prev_state)

    exec(ast.unparse(ast_parsed), namespace)

    for name, node in visitor.global_vars.items():
        state[name] = namespace.get(name)

    for name, node in visitor.global_funcs.items():
        state[name] = namespace.get(name)

    # Serialize all the global objects together to maintain object refs
    with open(f"{args.output}.pickle", "wb") as f:
        dill.dump(state, f, recurse=True)
