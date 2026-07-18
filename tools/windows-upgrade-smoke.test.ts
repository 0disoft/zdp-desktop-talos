import { describe, expect, test } from "bun:test";
import path from "node:path";
import { argumentsMap, ownedPath } from "./windows-upgrade-smoke";

describe("Windows upgrade smoke guard", () => {
  test("accepts only a unique value for each named argument", () => {
    expect(argumentsMap(["--old-run-id", "101", "--new-run-id", "202"])).toEqual(
      new Map([
        ["--old-run-id", "101"],
        ["--new-run-id", "202"],
      ]),
    );
    expect(() => argumentsMap(["--old-run-id", "101", "--old-run-id", "202"])).toThrow("INVALID_ARGUMENTS");
    expect(() => argumentsMap(["old-run-id", "101"])).toThrow("INVALID_ARGUMENTS");
  });

  test("keeps package and evidence paths beneath runner temp", () => {
    const root = path.resolve("runner-temp");
    expect(ownedPath(root, path.join(root, "talos-old"))).toBe(path.join(root, "talos-old"));
    expect(() => ownedPath(root, root)).toThrow("PATH_OUTSIDE_RUNNER_TEMP");
    expect(() => ownedPath(root, path.join(root, "..", "outside"))).toThrow("PATH_OUTSIDE_RUNNER_TEMP");
  });
});
