<script lang="ts">
  import { onDestroy } from 'svelte';
  import { listTaskContracts, type TaskDetail } from '../../lib/api/task';
  let { disabled, onselect }: { disabled: boolean; onselect: (detail: TaskDetail) => void } = $props();
  let tasks = $state<TaskDetail[]>([]);
  let selected = $state('');
  let busy = $state(false);
  let loaded = $state(false);
  let error = $state('');
  let disposed = false;
  onDestroy(() => { disposed = true; });
  async function refresh() {
    busy = true; error = '';
    try {
      const result = await listTaskContracts();
      if (disposed) return;
      if (result.error) { tasks = []; error = result.error.message; }
      else { tasks = result.tasks; selected = tasks[0]?.task.task_id ?? ''; loaded = true; }
    } catch { if (!disposed) error = '기존 작업을 불러오지 못했습니다.'; }
    finally { if (!disposed) busy = false; }
  }
</script>

<div class="task-actions">
  <button type="button" class="secondary" onclick={refresh} disabled={disabled || busy}>{busy ? '불러오는 중…' : '기존 작업 불러오기'}</button>
  {#if error}<p role="alert">{error}</p>{/if}
  {#if tasks.length > 0}
    <label><span>현재 저장소·baseline의 최근 작업 · 최대 50개</span>
      <select bind:value={selected} disabled={disabled || busy}>
        {#each tasks as detail (detail.task.task_id)}
          <option value={detail.task.task_id}>{detail.goal.slice(0, 100)} · r{detail.task.revision} · {detail.task.status}</option>
        {/each}
      </select>
    </label>
    <button type="button" class="secondary" disabled={disabled || busy || !selected} onclick={() => { const detail = tasks.find((item) => item.task.task_id === selected); if (detail) onselect(detail); }}>선택한 작업 열기</button>
  {:else if loaded && !error}<p>현재 저장소·baseline에 저장된 작업이 없습니다.</p>{/if}
</div>
