import type { TaskDetail, TaskPageInput } from '../../lib/api/task';
import type { TalosError } from '../../lib/api/vault';

export const SEARCH_PAGE_LIMIT = 5;
type Page = { tasks: TaskDetail[]; next_cursor: string; error?: TalosError };

// Cancellation is cooperative: an in-flight Wails call is not aborted.
export async function searchTaskPages(
  input: TaskPageInput,
  fetchPage: (input: TaskPageInput) => Promise<Page>,
  signal: AbortSignal,
  progress: (pages: number) => void,
): Promise<Page | undefined> {
  const limit = input.query.trim() ? SEARCH_PAGE_LIMIT : 1;
  let cursor = input.cursor;
  const visited = new Set<string>();
  for (let pages = 1; pages <= limit; pages++) {
    if (signal.aborted) return;
    if (visited.has(cursor)) throw new Error('TASK_CURSOR_REPEATED');
    visited.add(cursor);
    const result = await fetchPage({ ...input, cursor });
    if (signal.aborted) return;
    progress(pages);
    if (result.error || result.tasks.length || !result.next_cursor || pages === limit) return result;
    cursor = result.next_cursor;
  }
}
