<script lang="ts">
  import { onDestroy } from 'svelte';
  import { listTaskContracts, type TaskDetail } from '../../lib/api/task';
  import { searchTaskPages, SEARCH_PAGE_LIMIT } from './search';
  let { disabled, baseline, onselect }: { disabled: boolean; baseline: string; onselect: (detail: TaskDetail) => void } = $props();
  let tasks = $state<TaskDetail[]>([]);
  let selected = $state('');
  let busy = $state(false);
  let loaded = $state(false);
  let error = $state('');
  let status = $state('');
  let query = $state('');
  let allBaselines = $state(false);
  let nextCursor = $state('');
  let cursors = $state<string[]>(['']);
  let page = $state(0);
  let applied = $state({status:'', query:'', all_baselines:false});
  let selectedDetail = $derived(tasks.find((item) => item.task.task_id === selected));
  let disposed = false;
  let controller: AbortController | undefined;
  let scannedPages = $state(0);
  let stopping = $state(false);
  let notice = $state('');
  onDestroy(() => { disposed = true; controller?.abort(); });
  function stop() {
    stopping = true;
    controller?.abort();
  }
  async function refresh(cursor = '', targetPage = 0, reset = true) {
    if (busy || disabled) return;
    busy = true; error = ''; notice = ''; scannedPages = 0; stopping = false;
    const request = new AbortController();
    controller = request;
    const filters = reset ? {status, query:query.trim(), all_baselines:allBaselines} : applied;
    try {
      const result = await searchTaskPages({...filters, cursor}, listTaskContracts, request.signal, (pages) => { scannedPages = pages; });
      if (disposed) return;
      if (!result) { notice = '탐색을 중단했습니다. 작업 찾기를 누르면 처음부터 검색합니다.'; }
      else if (result.error) { error = result.error.message; }
      else { tasks = result.tasks; selected = tasks[0]?.task.task_id ?? ''; loaded = true; nextCursor = result.next_cursor; page = targetPage; applied=filters; if(reset) cursors=['']; else cursors[targetPage]=cursor; }
    } catch { if (!disposed) { if (request.signal.aborted) notice = '탐색을 중단했습니다.'; else error = '기존 작업을 불러오지 못했습니다.'; } }
    finally { if (!disposed) { busy = false; stopping = false; controller = undefined; } }
  }
</script>

<div class="task-actions">
  <label><span>작업 상태</span><select bind:value={status} disabled={disabled || busy}><option value="">전체</option><option value="contracted">진행 중</option><option value="completed">적용 완료</option><option value="discarded">폐기 완료</option></select></label>
  <label><span>목표 검색</span><input bind:value={query} maxlength="120" disabled={disabled || busy} placeholder="검색할 단어" /></label>
  <label><input type="checkbox" bind:checked={allBaselines} disabled={disabled || busy} /> 이전 baseline 작업도 보기</label>
  <button type="button" class="secondary" onclick={() => refresh()} disabled={disabled || busy}>{busy ? '불러오는 중…' : '작업 찾기'}</button>
  {#if busy}
    <button type="button" class="secondary" onclick={stop} disabled={stopping}>탐색 중단</button>
    <p role="status">{stopping ? '중단 요청됨 · 현재 조회가 끝나면 멈춥니다.' : `탐색 중 · ${scannedPages}묶음 확인`}</p>
  {/if}
  {#if notice}<p role="status">{notice}</p>{/if}
  {#if error}<p role="alert">{error}</p>{/if}
  {#if tasks.length > 0}
    <label><span>현재 저장소의 작업 · {page + 1}번째 묶음</span>
      <select bind:value={selected} disabled={disabled || busy}>
        {#each tasks as detail (detail.task.task_id)}
          <option value={detail.task.task_id}>{detail.goal.slice(0, 100)} · r{detail.task.revision} · {detail.task.status === 'contracted' ? '진행 중' : detail.task.status === 'completed' ? '적용 완료' : '폐기 완료'}{detail.task.baseline_commit !== baseline ? ' · 읽기 전용' : ''}</option>
        {/each}
      </select>
    </label>
    {#if selectedDetail?.task.baseline_commit !== baseline}
      <p>baseline이 달라 읽기만 가능합니다. 실행하려면 해당 baseline의 저장소를 열거나 새 작업을 작성해 주세요.</p>
      <p>{selectedDetail?.goal}</p>
      <pre>{selectedDetail?.allowed_paths.join('\n')}</pre>
      <p>{selectedDetail?.acceptance_criteria.join(' · ')}</p>
    {:else}
      <button type="button" class="secondary" disabled={disabled || busy || !selected} onclick={() => { if (selectedDetail) onselect(selectedDetail); }}>선택한 작업 열기</button>
    {/if}
  {:else if loaded && !error}<p>{nextCursor ? '이 묶음에는 일치하는 작업이 없습니다. 다음 묶음에서 계속 찾을 수 있습니다.' : '일치하는 작업이 없습니다.'}</p>{/if}
  {#if loaded}
    <small>목표는 평문으로 색인하지 않고, 생성일 기준 50개씩 확인합니다. 검색어가 있으면 한 번에 최대 {SEARCH_PAGE_LIMIT}묶음까지 탐색합니다.</small>
    <div>
      <button type="button" class="secondary" disabled={disabled || busy || page === 0} onclick={() => refresh(cursors[page-1], page-1, false)}>이전</button>
      <button type="button" class="secondary" disabled={disabled || busy || !nextCursor} onclick={() => refresh(nextCursor, page+1, false)}>다음 묶음 찾기</button>
    </div>
  {/if}
</div>
