import { expect, test } from 'bun:test';
import { searchTaskPages, SEARCH_PAGE_LIMIT } from './search';

const input = { cursor: '', status: 'contracted', query: 'needle', all_baselines: false };
const empty = (next_cursor) => ({ tasks: [], next_cursor });

test('skips empty pages sequentially and stops at a match', async () => {
  const calls = [], progress = [];
  const match = { tasks: [{ goal: 'needle' }], next_cursor: 'third' };
  const result = await searchTaskPages(input, async (request) => {
    calls.push(request);
    return calls.length === 1 ? empty('second') : match;
  }, new AbortController().signal, (pages) => progress.push(pages));
  expect(result).toBe(match);
  expect(calls).toEqual([input, { ...input, cursor: 'second' }]);
  expect(progress).toEqual([1, 2]);
});

test('caps scanning and preserves continuation', async () => {
  let calls = 0;
  const result = await searchTaskPages(input, async () => empty(String(++calls)), new AbortController().signal, () => {});
  expect(calls).toBe(SEARCH_PAGE_LIMIT);
  expect(result.next_cursor).toBe(String(SEARCH_PAGE_LIMIT));
});

test('blank queries fetch only one page', async () => {
  let calls = 0;
  await searchTaskPages({ ...input, query: ' ' }, async () => { calls++; return empty('more'); }, new AbortController().signal, () => {});
  expect(calls).toBe(1);
});

test('cancellation discards an in-flight response and prevents another request', async () => {
  const controller = new AbortController();
  let resolve, calls = 0;
  const pending = new Promise((done) => { resolve = done; });
  const result = searchTaskPages(input, () => { calls++; return pending; }, controller.signal, () => { throw Error('stale progress'); });
  controller.abort();
  resolve(empty('more'));
  expect(await result).toBeUndefined();
  expect(calls).toBe(1);
});

test('already cancelled searches do not fetch', async () => {
  const controller = new AbortController();
  controller.abort();
  expect(await searchTaskPages(input, () => { throw Error('unexpected fetch'); }, controller.signal, () => {})).toBeUndefined();
});

test('end, service errors and thrown failures stop scanning', async () => {
  for (const page of [empty(''), { ...empty('more'), error: { message: 'unavailable' } }]) {
    let calls = 0;
    expect(await searchTaskPages(input, async () => { calls++; return page; }, new AbortController().signal, () => {})).toBe(page);
    expect(calls).toBe(1);
  }
  await expect(searchTaskPages(input, async () => { throw Error('offline'); }, new AbortController().signal, () => {})).rejects.toThrow('offline');
});

test('repeated continuation cannot loop', async () => {
  await expect(searchTaskPages(input, async () => empty('same'), new AbortController().signal, () => {})).rejects.toThrow('TASK_CURSOR_REPEATED');
});
