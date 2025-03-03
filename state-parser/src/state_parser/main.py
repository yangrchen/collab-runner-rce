import argparse
import pathlib

import dill


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
        default=[],
    )
    args = parser.parse_args()

    namespace = {}

    for pickled_state in args.state_files:
        with open(pickled_state, "rb") as f:
            prev_state = dill.load(f)
            namespace.update(prev_state)

    exec(args.file.read(), namespace)
    args.file.close()

    # Serialize all the global objects together to maintain object refs
    with open(f"{args.output}.pickle", "wb") as f:
        dill.dump(namespace, f, recurse=True)
