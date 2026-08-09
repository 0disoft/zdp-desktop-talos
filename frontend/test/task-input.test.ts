import { describe, expect, test } from 'bun:test';

import { argumentLines, uniqueLines } from '../src/lib/task-input';

describe('task contract line parsing', () => {
  test('deduplicates set-like contract fields after trimming', () => {
    expect(uniqueLines(' internal/** \n\nREADME.md\r\ninternal/**')).toEqual(['internal/**', 'README.md']);
  });

  test('preserves argument order and duplicate values', () => {
    expect(argumentLines('test\n-count=1\n-count=1\n./pkg\n./pkg')).toEqual([
      'test',
      '-count=1',
      '-count=1',
      './pkg',
      './pkg',
    ]);
  });
});
