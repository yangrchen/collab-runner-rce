import argparse
import ast
import pathlib
import pickle
from pure_eval import Evaluator


class GlobalScopeParser(ast.NodeVisitor):
    def __init__(self):
        self.global_funcs = {}
        self.global_vars = {}
        self.context = [("global", ())]

    def visit_FunctionDef(self, node: ast.FunctionDef) -> None:
        if self.context[-1][0] == "global":
            self.global_funcs[node.name] = node

        self.context.append(("function", set()))
        self.generic_visit(node)
        self.context.pop()

    def visit_Call(self, node: ast.Call) -> None:
        self.context.append(("call", set()))
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
    args = parser.parse_args()

    ast_parsed = ast.parse(args.file.read())
    args.file.close()

    visitor = GlobalScopeParser()
    visitor.visit(ast_parsed)

    names = {}
    exec(ast.unparse(ast_parsed), names)
    for nodes, value in Evaluator(names).interesting_expressions_grouped(ast_parsed):
        target = nodes[0]

        match type(target):
            case ast.Name:
                if target.id in visitor.global_vars:
                    with open(f"var_{target.id}.pickle", "wb") as f:
                        pickle.dump(names.get(target.id), f)
            case ast.FunctionDef:
                if target.name in visitor.global_funcs:
                    with open(f"func_{target.name}.pickle", "wb") as f:
                        pickle.dump(names.get(target.name), f)
