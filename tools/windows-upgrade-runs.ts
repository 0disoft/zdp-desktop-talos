import { appendFile } from "node:fs/promises";

const commitPattern = /^[a-f0-9]{40}$/;
const runIDPattern = /^[1-9]\d*$/;
const repositoryPattern = /^[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+$/;
const signingWorkflowPath = ".github/workflows/windows-signing.yml";

export type TrustedRun = {
  id: string;
  sourceCommit: string;
};

export type RunSelection = {
  oldRun: TrustedRun;
  newRun: TrustedRun;
  verifierCommit: string;
};

type FetchLike = (input: string, init?: RequestInit) => Promise<Response>;

function object(value: unknown, label: string): Record<string, unknown> {
  if (value === null || typeof value !== "object" || Array.isArray(value)) {
    throw new Error(`${label} must be an object`);
  }
  return value as Record<string, unknown>;
}

export function validateSigningRun(value: unknown, expectedRunID: string): TrustedRun {
  const run = object(value, "workflow run");
  if (!runIDPattern.test(expectedRunID) || String(run.id) !== expectedRunID) {
    throw new Error("workflow run ID does not match the requested run");
  }
  if (
    run.path !== signingWorkflowPath ||
    run.event !== "workflow_dispatch" ||
    run.status !== "completed" ||
    run.conclusion !== "success" ||
    run.head_branch !== "main" ||
    typeof run.head_sha !== "string" ||
    !commitPattern.test(run.head_sha)
  ) {
    throw new Error("workflow run is not a successful trusted main-branch signing run");
  }
  return { id: expectedRunID, sourceCommit: run.head_sha };
}

export function requireAheadOrIdentical(value: unknown, allowIdentical: boolean, label: string): void {
  const comparison = object(value, `${label} comparison`);
  const allowed = allowIdentical ? new Set(["ahead", "identical"]) : new Set(["ahead"]);
  if (typeof comparison.status !== "string" || !allowed.has(comparison.status)) {
    throw new Error(`${label} commit relationship is not trusted`);
  }
}

async function apiJSON(fetcher: FetchLike, url: string, token: string): Promise<unknown> {
  const response = await fetcher(url, {
    headers: {
      Accept: "application/vnd.github+json",
      Authorization: `Bearer ${token}`,
      "X-GitHub-Api-Version": "2022-11-28",
    },
  });
  if (!response.ok) {
    throw new Error(`GitHub API request failed with status ${response.status}`);
  }
  return response.json() as Promise<unknown>;
}

export async function resolveRunSelection(input: {
  repository: string;
  oldRunID: string;
  newRunID: string;
  verifierCommit: string;
  token: string;
  fetcher?: FetchLike;
}): Promise<RunSelection> {
  if (!repositoryPattern.test(input.repository) || !runIDPattern.test(input.oldRunID) || !runIDPattern.test(input.newRunID)) {
    throw new Error("repository or workflow run ID is invalid");
  }
  if (!commitPattern.test(input.verifierCommit) || input.token.trim() === "") {
    throw new Error("verifier commit and GitHub token are required");
  }
  if (input.oldRunID === input.newRunID) {
    throw new Error("old and new signing runs must differ");
  }
  const fetcher = input.fetcher ?? fetch;
  const base = `https://api.github.com/repos/${input.repository}`;
  const [oldValue, newValue] = await Promise.all([
    apiJSON(fetcher, `${base}/actions/runs/${input.oldRunID}`, input.token),
    apiJSON(fetcher, `${base}/actions/runs/${input.newRunID}`, input.token),
  ]);
  const oldRun = validateSigningRun(oldValue, input.oldRunID);
  const newRun = validateSigningRun(newValue, input.newRunID);
  if (oldRun.sourceCommit === newRun.sourceCommit) {
    throw new Error("old and new signing runs resolve to the same source commit");
  }
  const oldToNew = await apiJSON(fetcher, `${base}/compare/${oldRun.sourceCommit}...${newRun.sourceCommit}`, input.token);
  requireAheadOrIdentical(oldToNew, false, "old-to-new");
  const newToVerifier = await apiJSON(fetcher, `${base}/compare/${newRun.sourceCommit}...${input.verifierCommit}`, input.token);
  requireAheadOrIdentical(newToVerifier, true, "new-to-verifier");
  return { oldRun, newRun, verifierCommit: input.verifierCommit };
}

function argumentsMap(arguments_: string[]): Map<string, string> {
  const result = new Map<string, string>();
  for (let index = 0; index < arguments_.length; index += 2) {
    const key = arguments_[index];
    const value = arguments_[index + 1];
    if (!key?.startsWith("--") || value === undefined || value.startsWith("--") || result.has(key)) {
      throw new Error("invalid or duplicate command arguments");
    }
    result.set(key, value);
  }
  return result;
}

async function main(): Promise<void> {
  const args = argumentsMap(Bun.argv.slice(2));
  const outputPath = process.env.GITHUB_OUTPUT;
  if (process.env.CI !== "true" || !outputPath) {
    throw new Error("trusted run resolution is restricted to GitHub Actions CI");
  }
  const selection = await resolveRunSelection({
    repository: args.get("--repository") ?? "",
    oldRunID: args.get("--old-run-id") ?? "",
    newRunID: args.get("--new-run-id") ?? "",
    verifierCommit: args.get("--verifier-commit") ?? "",
    token: process.env.GITHUB_TOKEN ?? "",
  });
  await appendFile(
    outputPath,
    [
      `old_source_commit=${selection.oldRun.sourceCommit}`,
      `new_source_commit=${selection.newRun.sourceCommit}`,
      `verifier_commit=${selection.verifierCommit}`,
      "",
    ].join("\n"),
    { encoding: "utf8" },
  );
}

if (import.meta.main) {
  try {
    await main();
  } catch (error) {
    console.error(error instanceof Error ? error.message : "trusted run resolution failed");
    process.exitCode = 1;
  }
}
