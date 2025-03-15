import argparse
import pathlib
import sys

import dill


def is_valid_file(parser: argparse.ArgumentParser, arg):
    if not pathlib.Path(arg).is_file():
        parser.error(f"The file {arg} does not exist")
    elif pathlib.Path(arg).suffix != ".py":
        parser.error("Input file should be a Python file")

    return arg


def run_file(input_path: str, state_files: list[str], output_name: str):
    try:
        namespace = {}
        for file in state_files:
            with open(file, "rb") as f:
                prev_state = dill.load(f)
                namespace.update(prev_state)

        with open(input_path, "r") as f:
            exec(f.read(), namespace)

        # Serialize all the global objects together to maintain object refs
        with open(f"{output_name}.pickle", "wb") as f:
            dill.dump(namespace, f, recurse=True)
    except Exception as e:
        print(f"{type(e).__name__}: {e}", file=sys.stderr)
        sys.exit(1)


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

    run_file(
        input_path=args.file, state_files=args.state_files, output_name=args.output
    )
