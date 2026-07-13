<script lang="ts">
  import { onMount } from 'svelte';
  import {
    createVault,
    getVaultStatus,
    hardPurgeVault,
    listVaults,
    lockVault,
    openVault,
    updateVaultRetention,
    type TalosError,
    type VaultSummary,
    type VaultStatus,
  } from './lib/api/vault';
  import { closeWorkspace, inspectRepository, type WorkspaceStatus } from './lib/api/workspace';
  import { createTaskContract, reviseTaskContract, type TaskStatus } from './lib/api/task';
  import { answerDecision, listDecisions, resolveDecisionConflict, type DecisionItem } from './lib/api/decision';
  import { listPermissionRequests, resolvePermissionRequest, type PermissionOutcome, type PermissionRequest } from './lib/api/permission';
  import { executeVerification, type ExecutionStatus } from './lib/api/execution';

  let vault = $state<VaultStatus>({ state: 'locked', persistent_key_store: false });
  let retentionDays = $state(30);
  let loading = $state(true);
  let latestError = $state<TalosError | null>(null);
  let vaults = $state<VaultSummary[]>([]);
  let selectedVaultID = $state('');
  let creatingNew = $state(false);
  let purgeOpen = $state(false);
  let purgeConfirmation = $state('');
  let workspace = $state<WorkspaceStatus>({ state: 'closed' });
  let workspacePath = $state('');
  let task = $state<TaskStatus | null>(null);
  let taskGoal = $state('');
  let taskPaths = $state('');
  let taskCriteria = $state('');
  let taskVerificationRule = $state('go-test');
  let taskVerificationArguments = $state('test\n./...');
  let taskVerificationDirectory = $state('.');
  let taskRisk = $state<'low' | 'medium' | 'high'>('medium');
  let editingTask = $state(false);
  let decisions = $state<DecisionItem[]>([]);
  let decisionDrafts = $state<Record<string, string>>({});
  let permissionRequests = $state<PermissionRequest[]>([]);
  let execution = $state<ExecutionStatus | null>(null);

  onMount(async () => {
    try {
      const [status, catalog] = await Promise.all([getVaultStatus(), listVaults()]);
      vault = status;
      if (status.state === 'unlocked') retentionDays = status.retention_days ?? retentionDays;
      if (catalog.error) {
        latestError = catalog.error;
      } else {
        vaults = catalog.vaults;
        selectedVaultID = catalog.vaults.at(-1)?.vault_id ?? '';
      }
    } catch {
      latestError = localError('VAULT_STATUS_UNAVAILABLE', 'Vault 상태를 불러오지 못했습니다.');
    } finally {
      loading = false;
    }
  });

  async function handleCreateOrLock() {
    loading = true;
    latestError = null;
    try {
      const result = vault.state === 'unlocked' ? await lockVault() : await createVault(retentionDays);
      if (result.error) {
        latestError = result.error;
      } else if (result.vault) {
        vault = result.vault;
        if (vault.state === 'locked') clearPrivateTaskState();
        if (vault.state === 'unlocked') retentionDays = vault.retention_days ?? retentionDays;
        if (vault.state === 'unlocked') {
          const catalog = await listVaults();
          if (catalog.error) {
            latestError = catalog.error;
          } else {
            vaults = catalog.vaults;
            selectedVaultID = vault.vault_id ?? selectedVaultID;
            creatingNew = false;
          }
        }
      }
    } catch {
      latestError = localError('VAULT_REQUEST_FAILED', 'Vault 요청을 완료하지 못했습니다.');
    } finally {
      loading = false;
    }
  }

  async function handleOpen() {
    if (!selectedVaultID) return;
    loading = true;
    latestError = null;
    try {
      const result = await openVault(selectedVaultID);
      if (result.error) latestError = result.error;
      else if (result.vault) {
        vault = result.vault;
        retentionDays = result.vault.retention_days ?? retentionDays;
      }
    } catch {
      latestError = localError('VAULT_REQUEST_FAILED', 'Vault를 열지 못했습니다.');
    } finally {
      loading = false;
    }
  }

  async function handleRetentionUpdate() {
    if (vault.state !== 'unlocked' || !vault.revision) return;
    loading = true;
    latestError = null;
    try {
      const result = await updateVaultRetention(retentionDays, vault.revision);
      if (result.error) latestError = result.error;
      else if (result.vault) vault = result.vault;
    } catch {
      latestError = localError('VAULT_REQUEST_FAILED', '보존 기간을 변경하지 못했습니다.');
    } finally {
      loading = false;
    }
  }

  async function handleHardPurge() {
    if (vault.state !== 'unlocked' || !vault.revision || purgeConfirmation !== vault.vault_id) return;
    loading = true;
    latestError = null;
    try {
      const result = await hardPurgeVault(vault.revision, purgeConfirmation);
      if (result.error) {
        latestError = result.error;
        if (result.error.code === 'VAULT_PURGE_INCOMPLETE') {
          vault = { state: 'locked', persistent_key_store: vault.persistent_key_store };
          clearPrivateTaskState();
        }
      } else if (result.vault) {
        vault = result.vault;
        clearPrivateTaskState();
      }
      const catalog = await listVaults();
      if (catalog.error) latestError ??= catalog.error;
      else {
        vaults = catalog.vaults;
        selectedVaultID = catalog.vaults.at(-1)?.vault_id ?? '';
      }
      purgeOpen = false;
      purgeConfirmation = '';
    } catch {
      latestError = localError('VAULT_PURGE_STATUS_UNKNOWN', '완전 삭제 상태를 확인하지 못했습니다. 앱을 다시 시작해 주세요.');
      vault = { state: 'locked', persistent_key_store: vault.persistent_key_store };
      clearPrivateTaskState();
    } finally {
      loading = false;
    }
  }

  async function handleWorkspace() {
    loading = true;
    latestError = null;
    try {
      const result = workspace.state === 'open' ? await closeWorkspace() : await inspectRepository(workspacePath.trim());
      if (result.error) latestError = result.error;
      else if (result.workspace) {
        workspace = result.workspace;
        if (workspace.state === 'open') workspacePath = workspace.root ?? workspacePath;
      }
    } catch {
      latestError = localError('WORKSPACE_REQUEST_FAILED', 'Git 저장소 상태를 확인하지 못했습니다.');
    } finally {
      loading = false;
    }
  }

  async function handleTaskContract() {
    if (vault.state !== 'unlocked' || workspace.state !== 'open' || workspace.dirty) return;
    loading = true;
    latestError = null;
    try {
      const input = {
        goal: taskGoal.trim(),
        allowed_paths: lines(taskPaths),
        forbidden_actions: ['git.push', 'git.commit', 'network.egress', 'dependency.install'],
        acceptance_criteria: lines(taskCriteria),
        verification_commands: [{
          rule_id: taskVerificationRule.trim(),
          arguments: lines(taskVerificationArguments),
          working_directory: taskVerificationDirectory.trim() || '.',
        }],
        risk: taskRisk,
      };
      const result = task
        ? await reviseTaskContract(task.task_id, task.revision, input)
        : await createTaskContract(input);
      if (result.error) {
        latestError = result.error;
        if (result.error.code === 'WORKSPACE_DIRTY' || result.error.code === 'WORKSPACE_BASELINE_CHANGED') {
          workspace = { state: 'closed' };
        }
      } else if (result.task) {
        task = result.task;
        execution = null;
        editingTask = false;
        await Promise.all([refreshDecisions(), refreshPermissions()]);
      }
    } catch {
      latestError = localError('TASK_REQUEST_FAILED', 'Task Contract를 저장하지 못했습니다.');
    } finally {
      loading = false;
    }
  }

  function lines(value: string): string[] {
    return [...new Set(value.split(/\r?\n/).map((item) => item.trim()).filter(Boolean))];
  }

  async function refreshDecisions() {
    if (!task || vault.state !== 'unlocked') { decisions = []; return; }
    const result = await listDecisions(task.task_id);
    if (result.error) latestError = result.error;
    else decisions = result.decisions;
  }

  async function refreshPermissions() {
    if (!task || vault.state !== 'unlocked') { permissionRequests = []; return; }
    const result = await listPermissionRequests(task.task_id);
    if (result.error) latestError = result.error;
    else permissionRequests = result.requests;
  }

  async function handlePermission(item: PermissionRequest, outcome: PermissionOutcome) {
    loading = true; latestError = null;
    try {
      const result = await resolvePermissionRequest(item.request_id, outcome);
      if (result.error) latestError = result.error;
      else permissionRequests = permissionRequests.filter((current) => current.request_id !== item.request_id);
    } catch {
      latestError = localError('PERMISSION_REQUEST_FAILED', '권한 선택을 저장하지 못했습니다.');
    } finally { loading = false; }
  }

  async function handleExecution() {
    if (!task || vault.state !== 'unlocked') return;
    loading = true; latestError = null;
    try {
      const result = await executeVerification(task.task_id);
      if (result.error) latestError = result.error;
      else if (result.execution) {
        execution = result.execution;
        if (result.execution.state === 'review_required') await refreshPermissions();
      }
    } catch {
      latestError = localError('EXECUTION_REQUEST_FAILED', '검증 실행 상태를 확인하지 못했습니다.');
    } finally { loading = false; }
  }

  async function handleDecisionAnswer(item: DecisionItem, optionID = '') {
    loading = true;
    latestError = null;
    try {
      const result = await answerDecision(item.decision_id, item.question_revision, optionID, optionID ? '' : (decisionDrafts[item.decision_id] ?? ''));
      if (result.error) latestError = result.error;
      else if (result.decision) {
        decisions = decisions.map((current) => current.decision_id === result.decision?.decision_id ? result.decision : current);
        decisionDrafts[item.decision_id] = '';
      }
    } catch {
      latestError = localError('DECISION_REQUEST_FAILED', 'Decision 답변을 저장하지 못했습니다.');
    } finally { loading = false; }
  }

  async function handleDecisionResolution(item: DecisionItem, answerID: string) {
    loading = true; latestError = null;
    try {
      const result = await resolveDecisionConflict(item.decision_id, item.question_revision, answerID);
      if (result.error) latestError = result.error;
      else if (result.decision) decisions = decisions.map((current) => current.decision_id === result.decision?.decision_id ? result.decision : current);
    } catch {
      latestError = localError('DECISION_RESOLUTION_FAILED', '충돌 답변을 확정하지 못했습니다.');
    } finally { loading = false; }
  }

  function clearPrivateTaskState() {
    task = null; decisions = []; permissionRequests = []; decisionDrafts = {}; execution = null; editingTask = false;
    taskGoal = ''; taskPaths = ''; taskCriteria = '';
    taskVerificationRule = 'go-test'; taskVerificationArguments = 'test\n./...'; taskVerificationDirectory = '.';
  }

  function localError(code: string, message: string): TalosError {
    return { code, message, retryable: false, correlation_id: '' };
  }
</script>

<svelte:head>
  <meta
    name="description"
    content="Talos Agent는 작업 계약과 검증 증거를 중심으로 움직이는 로컬 코딩 에이전트입니다."
  />
</svelte:head>

<main class="shell">
  <header class="masthead">
    <div class="brand" aria-label="Talos Agent">
      <span class="brand-mark" aria-hidden="true">T</span>
      <span>Talos Agent</span>
    </div>
    <span class="phase">Private Alpha</span>
  </header>

  <section class="hero" aria-labelledby="hero-title">
    <p class="eyebrow">LOCAL DEVELOPMENT PARTNER</p>
    <h1 id="hero-title">다음 작업까지 이어지는 기억</h1>
    <p class="lede">
      작업 계약, 사용자 결정, 패치와 검증 증거를 로컬에 남기고 필요한 순간에만 다시 사용합니다.
    </p>
  </section>

  <section class="status-grid" aria-label="Talos 상태">
    <article class="status-card vault-card">
      <div class="status-heading">
        <span class:unlocked={vault.state === 'unlocked'} class="status-dot locked" aria-hidden="true"></span>
        <h2>Vault</h2>
      </div>
      <strong>{loading ? '확인 중' : vault.state === 'unlocked' ? '열림' : '잠김'}</strong>
      <p>
        {vault.state === 'unlocked'
          ? `보존 기간 ${vault.retention_days}일 · revision ${vault.revision}`
          : vault.persistent_key_store
            ? '기기 전용 키 저장소를 사용할 수 있습니다.'
            : '안전한 키 저장소를 확인하고 있습니다.'}
      </p>
      <div class="vault-actions">
        {#if vault.state === 'locked' && vaults.length > 0 && !creatingNew}
          <label class="vault-picker">
            <span>기존 Vault</span>
            <select bind:value={selectedVaultID} disabled={loading}>
              {#each vaults as item}
                <option value={item.vault_id}>{new Date(item.created_at).toLocaleDateString('ko-KR')} 생성</option>
              {/each}
            </select>
          </label>
          <button type="button" onclick={handleOpen} disabled={loading || !selectedVaultID}>Vault 열기</button>
          <button type="button" class="secondary" onclick={() => (creatingNew = true)} disabled={loading}>새로 만들기</button>
        {:else if vault.state === 'locked'}
          <label>
            <span>보존 기간</span>
            <select bind:value={retentionDays} disabled={loading}>
              <option value={30}>30일</option>
              <option value={90}>90일</option>
              <option value={365}>365일</option>
            </select>
          </label>
          <button type="button" onclick={handleCreateOrLock} disabled={loading || !vault.persistent_key_store}>새 Vault 만들기</button>
          {#if vaults.length > 0}
            <button type="button" class="secondary" onclick={() => (creatingNew = false)} disabled={loading}>취소</button>
          {/if}
        {:else}
          <label>
            <span>보존 기간</span>
            <select bind:value={retentionDays} disabled={loading}>
              <option value={30}>30일</option>
              <option value={90}>90일</option>
              <option value={365}>365일</option>
            </select>
          </label>
          <button
            type="button"
            class="secondary"
            onclick={handleRetentionUpdate}
            disabled={loading || retentionDays === vault.retention_days}>보존 기간 저장</button
          >
          <button type="button" onclick={handleCreateOrLock} disabled={loading}>Vault 잠그기</button>
          {#if !purgeOpen}
            <button type="button" class="danger" onclick={() => (purgeOpen = true)} disabled={loading}>Vault 완전 삭제</button>
          {:else}
            <div class="purge-confirmation">
              <p>이 작업은 이 기기의 암호키와 Vault 데이터를 되돌릴 수 없게 제거합니다.</p>
              <code>{vault.vault_id}</code>
              <label>
                <span>확인하려면 Vault ID 입력</span>
                <input bind:value={purgeConfirmation} autocomplete="off" spellcheck="false" disabled={loading} />
              </label>
              <button type="button" class="danger" onclick={handleHardPurge} disabled={loading || purgeConfirmation !== vault.vault_id}>완전 삭제 실행</button>
              <button type="button" class="secondary" onclick={() => { purgeOpen = false; purgeConfirmation = ''; }} disabled={loading}>취소</button>
            </div>
          {/if}
        {/if}
      </div>
    </article>

    <article class="status-card workspace-card">
      <div class="status-heading">
        <span class:unlocked={workspace.state === 'open'} class="status-dot locked" aria-hidden="true"></span>
        <h2>Workspace</h2>
      </div>
      <strong>{workspace.state === 'open' ? (workspace.detached ? 'Detached HEAD' : workspace.head_ref) : '닫힘'}</strong>
      <p>
        {workspace.state === 'open'
          ? `${workspace.dirty ? `변경 ${workspace.change_count}개` : '변경 없음'} · ${workspace.baseline_commit?.slice(0, 12)}`
          : '로컬 Git 저장소를 작업 기준점으로 검사합니다.'}
      </p>
      <div class="workspace-actions">
        {#if workspace.state === 'closed'}
          <label>
            <span>저장소 폴더</span>
            <input bind:value={workspacePath} autocomplete="off" spellcheck="false" disabled={loading} placeholder="C:\workspace\repository" />
          </label>
          <button type="button" onclick={handleWorkspace} disabled={loading || !workspacePath.trim()}>저장소 검사</button>
        {:else}
          <code>{workspace.root}</code>
          <button type="button" class="secondary" onclick={handleWorkspace} disabled={loading}>Workspace 닫기</button>
        {/if}
      </div>
    </article>

    <article class="status-card task-card">
      <div class="status-heading">
        <span class:unlocked={task !== null} class="status-dot waiting" aria-hidden="true"></span>
        <h2>Task Contract</h2>
      </div>
      <strong>{task ? `확정 · revision ${task.revision}` : '작성 대기'}</strong>
      <p>{task ? `${task.risk} risk · ${task.baseline_commit.slice(0, 12)}` : '목표와 수정 범위, 완료 조건을 먼저 고정합니다.'}</p>
      {#if task && !editingTask}
        <div class="task-summary">
          <code>{task.task_id}</code>
          <button type="button" class="secondary" onclick={() => (editingTask = true)} disabled={loading}>계약 수정</button>
        </div>
      {:else}
        <div class="task-actions">
          <label><span>목표</span><textarea bind:value={taskGoal} rows="3" maxlength="4096" disabled={loading} placeholder="이번 작업에서 끝낼 한 가지 목표"></textarea></label>
          <label><span>수정 가능 경로 · 한 줄에 하나</span><textarea bind:value={taskPaths} rows="3" disabled={loading} placeholder="internal/domain/**"></textarea></label>
          <label><span>완료 조건 · 한 줄에 하나</span><textarea bind:value={taskCriteria} rows="3" disabled={loading} placeholder="관련 테스트가 통과한다"></textarea></label>
          <label><span>검증 규칙</span><input bind:value={taskVerificationRule} maxlength="64" autocomplete="off" spellcheck="false" disabled={loading} placeholder="go-test" /></label>
          <label><span>검증 인수 · 한 줄에 하나</span><textarea bind:value={taskVerificationArguments} rows="3" disabled={loading} placeholder={'test\n./...'}></textarea></label>
          <label><span>실행 폴더 · 저장소 기준</span><input bind:value={taskVerificationDirectory} maxlength="4096" autocomplete="off" spellcheck="false" disabled={loading} placeholder="." /></label>
          <label class="task-risk"><span>위험도</span><select bind:value={taskRisk} disabled={loading}><option value="low">낮음</option><option value="medium">보통</option><option value="high">높음</option></select></label>
          <button type="button" onclick={handleTaskContract} disabled={loading || vault.state !== 'unlocked' || workspace.state !== 'open' || workspace.dirty || !taskGoal.trim() || lines(taskPaths).length === 0 || lines(taskCriteria).length === 0 || !taskVerificationRule.trim()}>{task ? `revision ${task.revision + 1} 확정` : '계약 확정'}</button>
          {#if task}<button type="button" class="secondary" onclick={() => (editingTask = false)} disabled={loading}>취소</button>{/if}
          {#if vault.state !== 'unlocked' || workspace.state !== 'open' || workspace.dirty}
            <small>{vault.state !== 'unlocked' ? 'Vault를 먼저 열어 주세요.' : workspace.state !== 'open' ? 'Workspace를 먼저 열어 주세요.' : '커밋되지 않은 변경을 먼저 정리해 주세요.'}</small>
          {/if}
        </div>
      {/if}
    </article>

    <article class="status-card">
      <div class="status-heading">
        <span class:unlocked={execution?.state === 'succeeded'} class:error={execution === null && latestError?.code.startsWith('EXECUTION_')} class="status-dot waiting" aria-hidden="true"></span>
        <h2>Verification</h2>
      </div>
      <strong>{execution?.state === 'succeeded' ? '통과' : execution?.state === 'review_required' ? '권한 확인 대기' : '실행 대기'}</strong>
      <p>{execution?.state === 'succeeded' ? `revision ${execution.contract_revision} · ${execution.worktree_state_hash.slice(0, 12)} · ${execution.replayed ? '저장된 증거' : '새 증거'}` : execution?.state === 'review_required' ? '아래 Permission Review에서 실행 범위를 선택해 주세요.' : 'Task Contract에 확정한 첫 번째 검증 명령을 실행합니다.'}</p>
      <button type="button" onclick={handleExecution} disabled={loading || vault.state !== 'unlocked' || !task || editingTask}>
        {execution?.state === 'review_required' ? '승인 후 다시 실행' : execution?.state === 'succeeded' ? '다시 검증' : '검증 시작'}
      </button>
    </article>

    <article class="status-card decision-card">
      <div class="status-heading">
        <span class:error={decisions.some((item) => item.state === 'conflicted')} class:unlocked={decisions.some((item) => item.state === 'open')} class="status-dot clear" aria-hidden="true"></span>
        <h2>Decision Queue</h2>
      </div>
      <strong>{decisions.filter((item) => item.state === 'open').length}개 대기</strong>
      <p>{decisions.some((item) => item.state === 'conflicted') ? '충돌한 답변을 확인해야 합니다.' : '안전하게 계속할 수 없는 선택만 여기에 모입니다.'}</p>
      <div class="decision-list">
        {#each decisions as item (item.decision_id)}
          <section class:conflicted={item.state === 'conflicted'} class="decision-item">
            <div class="decision-meta"><span>{item.category}</span><span>{item.state}</span><span>rev {item.question_revision}</span></div>
            <h3>{item.question}</h3>
            <p>{item.reason}</p>
            <p class="decision-risk">답하지 않으면: {item.risk_if_unanswered}</p>
            <p class="decision-default">기본값: {item.safe_default.action}</p>
            {#if item.state === 'open'}
              <div class="decision-options">
                {#each item.options as option}
                  <button type="button" onclick={() => handleDecisionAnswer(item, option.id)} disabled={loading}><strong>{option.label}</strong><span>{option.consequence}</span></button>
                {/each}
              </div>
              <label class="decision-text"><span>직접 답변</span><textarea bind:value={decisionDrafts[item.decision_id]} rows="2" maxlength="4096" disabled={loading}></textarea></label>
              <button type="button" class="secondary" onclick={() => handleDecisionAnswer(item)} disabled={loading || !(decisionDrafts[item.decision_id] ?? '').trim()}>답변 저장</button>
            {:else if item.state === 'conflicted'}
              <div class="decision-conflicts">
                <p>서로 다른 답변이 들어왔습니다. 유지할 답변을 선택하세요.</p>
                {#each item.answers as answer (answer.answer_id)}
                  <button type="button" class="secondary" onclick={() => handleDecisionResolution(item, answer.answer_id)} disabled={loading}>
                    {answer.selected_option_id ?? answer.text}
                  </button>
                {/each}
              </div>
            {:else if item.answer}
              <p class="decision-answer">최근 답변: {item.answer.selected_option_id ?? item.answer.text}</p>
            {/if}
          </section>
        {:else}
          <p class="decision-empty">현재 대기 중인 질문이 없습니다.</p>
        {/each}
      </div>
    </article>

    <article class="status-card">
      <div class="status-heading">
        <span class:unlocked={permissionRequests.length > 0} class="status-dot waiting" aria-hidden="true"></span>
        <h2>권한 검토</h2>
      </div>
      <strong>{permissionRequests.length}개 대기</strong>
      <p>실행 파일과 인자를 확인한 뒤 이번 실행 또는 현재 작업에만 허용할 수 있습니다.</p>
      <div class="decision-list">
        {#each permissionRequests as item (item.request_id)}
          <section class="decision-item">
            <div class="decision-meta"><span>{item.rule_id}</span><span>{Math.ceil(item.timeout_ms / 1000)}초</span></div>
            <h3>{item.executable}</h3>
            <code>{item.arguments.join(' ')}</code>
            <div class="decision-options">
              <button type="button" onclick={() => handlePermission(item, 'allow_once')} disabled={loading}><strong>한 번 허용</strong><span>15분 안에 한 번만 실행</span></button>
              <button type="button" onclick={() => handlePermission(item, 'allow_task')} disabled={loading}><strong>현재 작업 허용</strong><span>24시간 동안 같은 요청 허용</span></button>
              <button type="button" class="danger" onclick={() => handlePermission(item, 'deny')} disabled={loading}><strong>거부</strong><span>24시간 동안 같은 요청 차단</span></button>
            </div>
          </section>
        {:else}
          <p class="decision-empty">현재 검토할 실행 권한이 없습니다.</p>
        {/each}
      </div>
    </article>

    <article class="status-card">
      <div class="status-heading">
        <span class="status-dot waiting" aria-hidden="true"></span>
        <h2>Worker</h2>
      </div>
      <strong>대기</strong>
      <p>작업 실행은 Task Contract와 권한 검사가 준비된 뒤 시작됩니다.</p>
    </article>

    <article class="status-card" aria-live="polite">
      <div class="status-heading">
        <span class:error={latestError !== null} class="status-dot clear" aria-hidden="true"></span>
        <h2>최근 오류</h2>
      </div>
      <strong>{latestError ? latestError.code : '없음'}</strong>
      <p>{latestError ? latestError.message : '로컬 보안 경계가 정상적으로 유지되고 있습니다.'}</p>
    </article>
  </section>

  <section class="boundary" aria-labelledby="boundary-title">
    <div>
      <p class="eyebrow">CURRENT BOUNDARY</p>
      <h2 id="boundary-title">계정 연결과 데이터 공유는 별개입니다</h2>
    </div>
    <p>
      ZDP 회원가입은 계정 식별과 동의만 연결합니다. 저장소, 프롬프트, 터미널 기록과 기억은 사용자가
      별도로 승인하지 않는 한 기기 밖으로 나가지 않습니다.
    </p>
  </section>
</main>
