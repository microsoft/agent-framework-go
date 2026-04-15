import argparse
import json


def main() -> None:
    parser = argparse.ArgumentParser(description="Convert a value using a multiplication factor.")
    parser.add_argument("--value", type=float, required=True, help="The numeric value to convert.")
    parser.add_argument("--factor", type=float, required=True, help="The conversion factor to apply.")
    args = parser.parse_args()

    result = round(args.value * args.factor, 4)
    print(json.dumps({"value": args.value, "factor": args.factor, "result": result}))


if __name__ == "__main__":
    main()