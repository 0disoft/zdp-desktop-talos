import { describe, expect, test } from "bun:test";
import { requireAheadOrIdentical, resolveRunSelection, validateSigningRun } from "./windows-upgrade-runs";

const oldCommit = "a".repeat(40);
const newCommit = "b".repeat(40);
const verifierCommit = "c".repeat(40);

function run(id: string, commit: string): Record<string, unknown> {
  return {
    id: Number(id),
    path: ".github/workflows/windows-signing.yml",
    event: "workflow_dispatch",
    status: "completed",
    conclusion: "success",
    head_branch: "main",
    head_sha: commit,
  };
}

describe("trusted Windows signing run", () => {
  test("accepts a successful manual main signing run", () => {
    expect(validateSigningRun(run("101", oldCommit), "101")).toEqual({ id: "101", sourceCommit: oldCommit });
  });

  test("rejects a pull request or failed run", () => {
    expect(() => validateSigningRun({ ...run("101", oldCommit), event: "pull_request" }, "101")).toThrow("not a successful trusted");
    expect(() => validateSigningRun({ ...run("101", oldCommit), conclusion: "failure" }, "101")).toThrow("not a successful trusted");
  });

  test("requires old-to-new to be strictly ahead", () => {
    expect(() => requireAheadOrIdentical({ status: "identical" }, false, "old-to-new")).toThrow("not trusted");
    expect(() => requireAheadOrIdentical({ status: "ahead" }, false, "old-to-new")).not.toThrow();
  });
});

describe("run selection", () => {
  test("binds both package runs to the verifier commit ancestry", async () => {
    const responses = [run("101", oldCommit), run("202", newCommit), { status: "ahead" }, { status: "ahead" }];
    const urls: string[] = [];
    const fetcher = async (input: string): Promise<Response> => {
      urls.push(input);
      const value = responses.shift();
      return new Response(JSON.stringify(value), { status: 200, headers: { "content-type": "application/json" } });
    };
    const result = await resolveRunSelection({
      repository: "0disoft/zdp-desktop-talos",
      oldRunID: "101",
      newRunID: "202",
      verifierCommit,
      token: "not-logged",
      fetcher,
    });
    expect(result).toEqual({ oldRun: { id: "101", sourceCommit: oldCommit }, newRun: { id: "202", sourceCommit: newCommit }, verifierCommit });
    expect(urls).toContain(`https://api.github.com/repos/0disoft/zdp-desktop-talos/compare/${oldCommit}...${newCommit}`);
    expect(urls).toContain(`https://api.github.com/repos/0disoft/zdp-desktop-talos/compare/${newCommit}...${verifierCommit}`);
  });

  test("rejects the same run on both sides", async () => {
    await expect(
      resolveRunSelection({
        repository: "0disoft/zdp-desktop-talos",
        oldRunID: "101",
        newRunID: "101",
        verifierCommit,
        token: "token",
        fetcher: async () => new Response("{}"),
      }),
    ).rejects.toThrow("must differ");
  });
});
