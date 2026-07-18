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
  import { getTaskReview, type PatchReview } from './lib/api/review';
  import { applyPatch, discardPatch } from './lib/api/patch';
  import { changeMemoryLifecycle, compileTaskMemories, explainTaskMemory, listMemories, reviewMemory, sweepExpiredMemories, type AppliedMemory, type MemoryItem, type MemoryReviewOutcome } from './lib/api/memory';
  import { getModelProviderStatus, proposePlan, type ModelProviderStatus, type PlanProposal } from './lib/api/plan';
  import { previewMemoryProjection, type ProjectionPreview } from './lib/api/projection';
  import AccountBoundary from './features/account/AccountBoundary.svelte';
  import VaultBackup from './features/backup/VaultBackup.svelte';
  import SyncWorkspace from './features/sync/SyncWorkspace.svelte';

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
  let patchReview = $state<PatchReview | null>(null);
  let discardConfirmation = $state(false);
  let memoryCandidates = $state<MemoryItem[]>([]);
  let appliedMemories = $state<AppliedMemory[]>([]);
  let memoryEligible = $state(0);
  let lifecycleMemories = $state<MemoryItem[]>([]);
  let memoryValidityDays = $state(90);
  let supersedeTargets = $state<Record<string, string>>({});
  let projectionPreview = $state<ProjectionPreview | null>(null);
  let selectedProjectionPath = $state('memory/memories.md');
  let modelProvider = $state<ModelProviderStatus>({ provider_key: 'openai-responses', credential_name: 'OPENAI_API_KEY', ready: false, reason_code: 'MODEL_PROVIDER_UNAVAILABLE' });
  let modelConsent = $state(false);
  let planProposal = $state<PlanProposal | null>(null);

  onMount(async () => {
    try {
      const providerStatus = getModelProviderStatus();
      const [status, catalog] = await Promise.all([getVaultStatus(), listVaults()]);
      vault = status;
      try {
        modelProvider = await providerStatus;
      } catch {
        modelProvider = { provider_key: 'openai-responses', credential_name: 'OPENAI_API_KEY', ready: false, reason_code: 'MODEL_PROVIDER_UNAVAILABLE' };
      }
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
        modelConsent = false;
        planProposal = null;
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
        modelConsent = false;
        planProposal = null;
        editingTask = false;
        await Promise.all([refreshDecisions(), refreshPermissions(), refreshPatchReview(), refreshMemories()]);
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

  async function refreshPatchReview() {
    if (!task || vault.state !== 'unlocked') { patchReview = null; return; }
    try {
      const result = await getTaskReview(task.task_id);
      if (result.error) {
        if (result.error.code === 'PATCH_REVIEW_NOT_READY') patchReview = null;
        else latestError = result.error;
      } else patchReview = result.review ?? null;
    } catch {
      latestError = localError('PATCH_REVIEW_REQUEST_FAILED', '패치 상태를 확인하지 못했습니다.');
    }
  }

  async function handlePatch(kind: 'apply' | 'discard') {
    if (!task || !patchReview || task.status !== 'contracted') return;
    loading = true; latestError = null;
    try {
      const result = kind === 'apply'
        ? await applyPatch(task.task_id, patchReview.contract_revision, patchReview.patch_hash)
        : await discardPatch(task.task_id, patchReview.contract_revision, patchReview.patch_hash);
      if (result.error) {
        latestError = result.error;
        if (result.error.code === 'PATCH_REVIEW_STALE' || result.error.code === 'PATCH_CONFLICT') await refreshPatchReview();
      } else if (result.command) {
        task = { ...task, status: result.command.task_status };
        discardConfirmation = false;
        if (result.command.task_status === 'discarded') patchReview = null;
        else await refreshPatchReview();
      }
    } catch {
      latestError = localError('PATCH_COMMAND_FAILED', '패치 처리 상태를 확인하지 못했습니다.');
    } finally { loading = false; }
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

  async function handleExecution(commandIndex = 0) {
    if (!task || vault.state !== 'unlocked') return;
    loading = true; latestError = null;
    try {
      const result = await executeVerification(task.task_id, commandIndex);
      if (result.error) latestError = result.error;
      else if (result.execution) {
        execution = result.execution;
        if (result.execution.state === 'review_required') await refreshPermissions();
        if (result.execution.state === 'succeeded') await refreshPatchReview();
      }
    } catch {
      latestError = localError('EXECUTION_REQUEST_FAILED', '검증 실행 상태를 확인하지 못했습니다.');
    } finally { loading = false; }
  }

  async function handlePlanProposal() {
    if (!task || vault.state !== 'unlocked' || workspace.state !== 'open' || !workspace.root || !workspace.baseline_commit || !modelProvider.ready || !modelProvider.model_key || !modelConsent) return;
    loading = true; latestError = null;
    try {
      const result = await proposePlan({
        taskID: task.task_id,
        providerKey: modelProvider.provider_key,
        modelKey: modelProvider.model_key,
        workspaceRoot: workspace.root,
        baselineCommit: workspace.baseline_commit,
        contractRevision: task.revision,
      });
      if (result.error) {
        latestError = result.error;
        if (result.error.code === 'MODEL_CONFIGURATION_CHANGED') {
          modelConsent = false;
          planProposal = null;
          modelProvider = await getModelProviderStatus();
        }
      } else {
        planProposal = result.proposal ?? null;
        modelConsent = false;
      }
    } catch {
      latestError = localError('MODEL_REQUEST_FAILED', '모델 계획 요청 상태를 확인하지 못했습니다. 로컬 기능은 계속 사용할 수 있습니다.');
    } finally { loading = false; }
  }

  function modelStatusMessage(status: ModelProviderStatus): string {
    switch (status.reason_code) {
      case 'MODEL_NAME_UNCONFIGURED': return 'TALOS_OPENAI_MODEL을 설정하면 계획 제안을 사용할 수 있습니다.';
      case 'MODEL_CREDENTIAL_UNAVAILABLE': return `${status.credential_name}을 안전한 환경에 설정해 주세요.`;
      case 'MODEL_EXECUTION_UNAVAILABLE': return '로컬 실행기가 준비되지 않아 계획 제안을 비활성화했습니다.';
      case 'MODEL_CONFIGURATION_INVALID': return '설정한 모델 이름을 확인해 주세요.';
      default: return '모델 계획은 설정 전까지 꺼져 있습니다.';
    }
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

  async function refreshMemories() {
    if (!task || vault.state !== 'unlocked') { memoryCandidates = []; appliedMemories = []; return; }
    try {
      const [candidateResult, contextResult] = await Promise.all([listMemories(), explainTaskMemory(task.task_id)]);
      if (candidateResult.error) latestError = candidateResult.error;
      else {
        memoryCandidates = candidateResult.memories.filter((item) => item.state === 'candidate');
        lifecycleMemories = candidateResult.memories.filter((item) => item.state !== 'candidate' && item.state !== 'rejected' && item.state !== 'quarantined');
      }
      if (contextResult.error) latestError = contextResult.error;
      else appliedMemories = contextResult.items;
    } catch {
      latestError = localError('MEMORY_REQUEST_FAILED', '기억 검토 상태를 불러오지 못했습니다.');
    }
  }

  async function handleMemoryCompile() {
    if (!task || vault.state !== 'unlocked') return;
    loading = true; latestError = null;
    try {
      const result = await compileTaskMemories(task.task_id);
      if (result.error) latestError = result.error;
      else {
        memoryEligible = result.eligible;
        await refreshMemories();
      }
    } catch {
      latestError = localError('MEMORY_COMPILATION_FAILED', 'Decision에서 기억 후보를 만들지 못했습니다.');
    } finally { loading = false; }
  }

  async function handleMemoryReview(item: MemoryItem, outcome: MemoryReviewOutcome) {
    loading = true; latestError = null;
    const reasons: Record<MemoryReviewOutcome, string> = { approved: '현재 프로젝트 규칙으로 승인함', rejected: '지속적으로 적용할 기억이 아님', quarantined: '출처 또는 내용에 보안 검토가 필요함' };
    try {
      const result = await reviewMemory(item.memory_id, item.revision, outcome, reasons[outcome], outcome === 'approved' ? memoryValidityDays : 0);
      if (result.error) latestError = result.error;
      else await refreshMemories();
    } catch {
      latestError = localError('MEMORY_REVIEW_FAILED', '기억 검토 결과를 저장하지 못했습니다.');
    } finally { loading = false; }
  }

  async function handleMemoryLifecycle(item: MemoryItem, nextState: 'approved' | 'stable' | 'stale' | 'deprecated' | 'superseded') {
    loading = true; latestError = null;
    const replacement = nextState === 'superseded' ? (supersedeTargets[item.memory_id] ?? '') : '';
    const reasons = { approved: '현재 근거를 다시 확인해 활성화함', stable: '반복 확인된 프로젝트 규칙으로 명시 승격함', stale: '현재 프로젝트 기준과 다시 확인이 필요함', deprecated: '더 이상 적용하지 않는 규칙으로 폐기함', superseded: '새 기억이 이 규칙을 대체함' };
    try {
      const result = await changeMemoryLifecycle(item.memory_id, item.revision, nextState, reasons[nextState], replacement, nextState === 'approved' ? memoryValidityDays : 0);
      if (result.error) latestError = result.error;
      else await refreshMemories();
    } catch {
      latestError = localError('MEMORY_LIFECYCLE_FAILED', '기억 상태를 변경하지 못했습니다.');
    } finally { loading = false; }
  }

  async function handleMemorySweep() {
    loading = true; latestError = null;
    try {
      const result = await sweepExpiredMemories();
      if (result.error) latestError = result.error;
      else {
        memoryCandidates = result.memories.filter((item) => item.state === 'candidate');
        lifecycleMemories = result.memories.filter((item) => item.state !== 'candidate' && item.state !== 'rejected' && item.state !== 'quarantined');
        await refreshMemories();
      }
    } catch {
      latestError = localError('MEMORY_SWEEP_FAILED', '만료된 기억 상태를 갱신하지 못했습니다.');
    } finally { loading = false; }
  }

  async function handleProjectionPreview() {
    if (vault.state !== 'unlocked') return;
    loading = true;
    latestError = null;
    try {
      const result = await previewMemoryProjection();
      if (result.error) latestError = result.error;
      else {
        projectionPreview = result;
        if (!result.files.some((file) => file.path === selectedProjectionPath)) selectedProjectionPath = result.files[0]?.path ?? '';
      }
    } catch {
      latestError = localError('PROJECTION_PREVIEW_FAILED', '기억 projection을 안전하게 만들지 못했습니다.');
    } finally {
      loading = false;
    }
  }


  function clearPrivateTaskState() {
    task = null; decisions = []; permissionRequests = []; decisionDrafts = {}; execution = null; patchReview = null; editingTask = false; discardConfirmation = false;
    memoryCandidates = []; lifecycleMemories = []; appliedMemories = []; memoryEligible = 0; supersedeTargets = {}; projectionPreview = null; modelConsent = false; planProposal = null;
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
      <strong>{task ? `${task.status === 'completed' ? '적용 완료' : task.status === 'discarded' ? '폐기 완료' : '확정'} · revision ${task.revision}` : '작성 대기'}</strong>
      <p>{task ? `${task.risk} risk · ${task.baseline_commit.slice(0, 12)}` : '목표와 수정 범위, 완료 조건을 먼저 고정합니다.'}</p>
      {#if task && !editingTask}
        <div class="task-summary">
          <code>{task.task_id}</code>
          <button type="button" class="secondary" onclick={() => (editingTask = true)} disabled={loading || task.status !== 'contracted'}>계약 수정</button>
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

    <article class="status-card plan-card">
      <div class="status-heading">
        <span class:unlocked={planProposal?.state === 'proposed'} class:error={!modelProvider.ready} class="status-dot waiting" aria-hidden="true"></span>
        <h2>계획 제안</h2>
      </div>
      <strong>{planProposal ? '검토 대기' : modelProvider.ready ? `${modelProvider.provider_key} · ${modelProvider.model_key}` : '설정 필요'}</strong>
      {#if !modelProvider.ready}
        <p>{modelStatusMessage(modelProvider)}</p>
      {:else if !planProposal}
        <p>Task Contract와 현재 Task에 적용된 승인 기억만 {modelProvider.provider_key}의 {modelProvider.model_key}로 보냅니다. 저장소 파일, 터미널 로그와 비밀값은 포함하지 않습니다.</p>
        <label class="egress-consent">
          <input type="checkbox" bind:checked={modelConsent} disabled={loading || !task || task.status !== 'contracted' || workspace.state !== 'open'} />
          <span>현재 Workspace · baseline · revision 기준의 외부 전송을 확인했습니다.</span>
        </label>
        <button type="button" onclick={handlePlanProposal} disabled={loading || !modelConsent || !task || task.status !== 'contracted' || workspace.state !== 'open'}>계획 요청</button>
        <small>키는 {modelProvider.credential_name}에서 호출 순간에만 읽으며 화면과 Vault에 저장하지 않습니다.</small>
      {:else}
        <p>{planProposal.summary}</p>
        <div class="plan-steps">
          {#each planProposal.steps as step (step.id)}
            <section>
              <div><strong>{step.purpose}</strong><span>검증 #{step.command_index + 1}</span></div>
              <button type="button" class="secondary" onclick={() => handleExecution(step.command_index)} disabled={loading || !task || task.status !== 'contracted'}>이 검증 실행</button>
            </section>
          {/each}
        </div>
        {#if planProposal.memories.length > 0}
          <div class="plan-memories">
            <h3>계획에 전달된 기억</h3>
            {#each planProposal.memories as item (`${item.memory_id}:${item.revision}`)}
              <p>{item.statement}<small>{item.reason}</small></p>
            {/each}
          </div>
        {/if}
        <small>전송 기록 {planProposal.receipt.receipt_id} · 입력 {planProposal.receipt.input_tokens} · 출력 {planProposal.receipt.output_tokens} tokens · 가림 {planProposal.receipt.redaction_count}건</small>
        <button type="button" class="secondary" onclick={() => (planProposal = null)} disabled={loading}>계획 닫기</button>
      {/if}
    </article>

    <article class="status-card">
      <div class="status-heading">
        <span class:unlocked={execution?.state === 'succeeded'} class:error={execution === null && latestError?.code.startsWith('EXECUTION_')} class="status-dot waiting" aria-hidden="true"></span>
        <h2>Verification</h2>
      </div>
      <strong>{execution?.state === 'succeeded' ? '통과' : execution?.state === 'review_required' ? '권한 확인 대기' : '실행 대기'}</strong>
      <p>{execution?.state === 'succeeded' ? `revision ${execution.contract_revision} · ${execution.worktree_state_hash.slice(0, 12)} · ${execution.replayed ? '저장된 증거' : '새 증거'}` : execution?.state === 'review_required' ? '아래 Permission Review에서 실행 범위를 선택해 주세요.' : 'Task Contract에 확정한 첫 번째 검증 명령을 실행합니다.'}</p>
      <button type="button" onclick={() => handleExecution()} disabled={loading || vault.state !== 'unlocked' || !task || task.status !== 'contracted' || editingTask}>
        {execution?.state === 'review_required' ? '승인 후 다시 실행' : execution?.state === 'succeeded' ? '다시 검증' : '검증 시작'}
      </button>
    </article>

    <article class="status-card patch-review-card">
      <div class="status-heading">
        <span class:unlocked={patchReview?.status === 'fresh'} class:error={patchReview?.status === 'stale'} class="status-dot waiting" aria-hidden="true"></span>
        <h2>Patch Review</h2>
      </div>
      <strong>{patchReview?.status === 'fresh' ? '최신 검증 완료' : patchReview?.status === 'stale' ? '다시 검증 필요' : patchReview?.status === 'unverified' ? '검증 기록 없음' : '패치 대기'}</strong>
      <p>{patchReview ? `${patchReview.changes.length}개 파일 · +${patchReview.diffs.reduce((sum, item) => sum + item.added_lines, 0)} −${patchReview.diffs.reduce((sum, item) => sum + item.deleted_lines, 0)} · ${patchReview.patch_hash.slice(0, 12)}` : '검증 실행 뒤 현재 패치와 증거를 비교합니다.'}</p>
      {#if patchReview}
        {#if patchReview.secret_findings > 0}
          <p class="patch-warning">비밀정보 의심 항목 {patchReview.secret_findings}개를 가렸습니다.</p>
        {/if}
        <div class="patch-list">
          {#each patchReview.diffs as diff (diff.path)}
            <section class="patch-file">
              <div class="patch-file-heading">
                <code>{diff.path}</code>
                <span>+{diff.added_lines} −{diff.deleted_lines}</span>
              </div>
              {#if diff.omitted_reason}
                <p>{diff.omitted_reason === 'binary' ? '바이너리 파일은 내용을 표시하지 않습니다.' : diff.omitted_reason === 'symlink' ? '심볼릭 링크는 내용을 표시하지 않습니다.' : diff.omitted_reason === 'file_limit' ? '표시 가능한 파일 수를 넘었습니다.' : '지원하지 않는 파일 형식입니다.'}</p>
              {:else if diff.text}
                <pre>{diff.text}</pre>
                {#if diff.truncated}<small>일부 내용만 표시합니다.</small>{/if}
              {:else}
                <p>표시할 텍스트 변경이 없습니다.</p>
              {/if}
            </section>
          {:else}
            <p class="decision-empty">변경된 파일이 없습니다.</p>
          {/each}
        </div>
        {#if task?.status === 'contracted'}
          <div class="patch-actions">
            <button type="button" onclick={() => handlePatch('apply')} disabled={loading || patchReview.status !== 'fresh' || patchReview.secret_findings > 0 || patchReview.changes.length === 0 || decisions.some((item) => item.category === 'blocking' && item.state !== 'answered')}>기본 작업 폴더에 적용</button>
            {#if discardConfirmation}
              <button type="button" class="danger" onclick={() => handlePatch('discard')} disabled={loading}>변경 폐기 확인</button>
              <button type="button" class="secondary" onclick={() => (discardConfirmation = false)} disabled={loading}>취소</button>
            {:else}
              <button type="button" class="danger" onclick={() => (discardConfirmation = true)} disabled={loading}>변경 폐기</button>
            {/if}
            <button type="button" class="secondary" onclick={refreshPatchReview} disabled={loading}>상태 새로고침</button>
          </div>
        {:else}
          <p class="decision-answer">{task?.status === 'completed' ? '기본 작업 폴더에 적용했습니다. 자동 커밋은 하지 않았습니다.' : '변경을 폐기했습니다.'}</p>
        {/if}
      {/if}
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

    <article class="status-card memory-card">
      <div class="status-heading">
        <span class:unlocked={memoryCandidates.length > 0} class="status-dot clear" aria-hidden="true"></span>
        <h2>기억 검토</h2>
      </div>
      <strong>{memoryCandidates.length}개 후보</strong>
      <p>{memoryEligible > 0 ? `최근 확인한 Decision ${memoryEligible}개` : '답변된 Decision만 검토 후보가 됩니다.'}</p>
      <div class="memory-toolbar">
        <label><span>승인 유효기간</span><select bind:value={memoryValidityDays} disabled={loading}><option value={30}>30일</option><option value={90}>90일</option><option value={365}>365일</option><option value={0}>만료 없음</option></select></label>
        <button type="button" onclick={handleMemoryCompile} disabled={loading || !task || !decisions.some((item) => item.state === 'answered')}>Decision에서 후보 만들기</button>
        <button type="button" class="secondary" onclick={refreshMemories} disabled={loading || !task}>새로고침</button>
        <button type="button" class="secondary" onclick={handleMemorySweep} disabled={loading || !task}>만료 상태 갱신</button>
      </div>
      <div class="decision-list">
        {#each memoryCandidates as item (item.memory_id)}
          <section class="decision-item">
            <div class="decision-meta"><span>{item.kind}</span><span>{item.scope}</span><span>신뢰도 {item.confidence}</span><span>rev {item.revision}</span></div>
            <h3>{item.statement}</h3>
            <p>{item.rationale}</p>
            <p class="memory-provenance">출처 {item.evidence_event_ids.length}개 · {item.source_actor}</p>
            {#if item.goal_terms.length > 0}<p class="memory-terms">적용 단어: {item.goal_terms.join(', ')}</p>{/if}
            <div class="memory-actions">
              <button type="button" onclick={() => handleMemoryReview(item, 'approved')} disabled={loading}>승인</button>
              <button type="button" class="secondary" onclick={() => handleMemoryReview(item, 'rejected')} disabled={loading}>거절</button>
              <button type="button" class="danger" onclick={() => handleMemoryReview(item, 'quarantined')} disabled={loading}>격리</button>
            </div>
          </section>
        {:else}
          <p class="decision-empty">검토할 기억 후보가 없습니다.</p>
        {/each}
      </div>
      <div class="memory-lifecycle-list">
        <h3>기억 수명주기</h3>
        {#each lifecycleMemories as item (item.memory_id)}
          <section>
            <div class="decision-meta"><span>{item.kind}</span><span>{item.state}</span><span>rev {item.revision}</span>{#if item.expires_at}<span>{new Date(item.expires_at).toLocaleDateString('ko-KR')}까지</span>{/if}</div>
            <p>{item.statement}</p>
            {#if item.superseded_by}<small>{item.superseded_by}로 대체됨</small>{/if}
            {#if item.state === 'approved'}<button type="button" class="secondary" onclick={() => handleMemoryLifecycle(item, 'stable')} disabled={loading}>stable 승격</button>{/if}
            {#if item.state === 'approved' || item.state === 'stable'}<button type="button" class="secondary" onclick={() => handleMemoryLifecycle(item, 'stale')} disabled={loading}>재확인 필요</button>{/if}
            {#if item.state === 'stale'}<button type="button" class="secondary" onclick={() => handleMemoryLifecycle(item, 'approved')} disabled={loading}>다시 승인</button>{/if}
            {#if item.state === 'approved' || item.state === 'stable' || item.state === 'stale'}
              <button type="button" class="danger" onclick={() => handleMemoryLifecycle(item, 'deprecated')} disabled={loading}>폐기</button>
              <label><span>대체 기억</span><select bind:value={supersedeTargets[item.memory_id]} disabled={loading}><option value="">선택</option>{#each lifecycleMemories.filter((candidate) => candidate.memory_id !== item.memory_id && (candidate.state === 'approved' || candidate.state === 'stable')) as candidate}<option value={candidate.memory_id}>{candidate.statement.slice(0, 72)}</option>{/each}</select></label>
              <button type="button" class="secondary" onclick={() => handleMemoryLifecycle(item, 'superseded')} disabled={loading || !supersedeTargets[item.memory_id]}>대체 확정</button>
            {/if}
          </section>
        {:else}
          <p class="decision-empty">수명주기를 관리할 기억이 없습니다.</p>
        {/each}
      </div>
      <div class="applied-memory-list">
        <h3>현재 Task에 적용되는 기억</h3>
        {#each appliedMemories as item (item.memory_id)}
          <section>
            <p>{item.statement}</p>
            <small>{item.reason} · rev {item.revision}</small>
          </section>
        {:else}
          <p class="decision-empty">현재 Task에 선택된 기억이 없습니다.</p>
        {/each}
      </div>
    </article>

    <article class="status-card projection-card">
      <div class="status-heading">
        <span class:unlocked={projectionPreview !== null && projectionPreview.complete} class:error={projectionPreview !== null && !projectionPreview.complete} class="status-dot clear" aria-hidden="true"></span>
        <h2>기억 Projection</h2>
      </div>
      <strong>{projectionPreview ? `${projectionPreview.included}개 공개 기억` : '미리보기 대기'}</strong>
      <p>private·sensitive 기억과 검토 전 후보는 구조적으로 제외합니다. 비밀정보 의심 항목이 하나라도 나오면 결과 전체를 만들지 않습니다.</p>
      <button type="button" onclick={handleProjectionPreview} disabled={loading || vault.state !== 'unlocked'}>Projection 미리보기</button>
      {#if projectionPreview}
        <div class="projection-summary">
          <span>민감도 제외 {projectionPreview.excluded_sensitivity}</span>
          <span>상태 제외 {projectionPreview.excluded_lifecycle}</span>
          <span>{projectionPreview.complete ? '전체 범위' : '최대 200개 미리보기'}</span>
          <code>{projectionPreview.bundle_sha256.slice(0, 16)}</code>
        </div>
        <label class="projection-picker"><span>파일</span><select bind:value={selectedProjectionPath} disabled={loading}>{#each projectionPreview.files as file (file.path)}<option value={file.path}>{file.path} · {file.size_bytes} bytes</option>{/each}</select></label>
        {#each projectionPreview.files.filter((file) => file.path === selectedProjectionPath) as file (file.path)}
          <section class="projection-preview">
            <div><code>{file.sha256}</code>{#if file.truncated}<span>일부만 표시</span>{/if}</div>
            <pre>{file.preview}</pre>
          </section>
        {/each}
      {/if}
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

  <AccountBoundary vaultState={vault.state} vaultID={vault.vault_id ?? ''} disabled={loading} onerror={(error) => latestError = error} />
  <VaultBackup vaultState={vault.state} vaultID={vault.vault_id ?? ''} vaultRevision={vault.revision ?? 0} disabled={loading} onerror={(error) => latestError = error} onvaultchange={(status) => { vault = status; retentionDays = status.retention_days ?? retentionDays; selectedVaultID = status.vault_id ?? selectedVaultID; }} />
  <SyncWorkspace vaultState={vault.state} vaultID={vault.vault_id ?? ''} disabled={loading} onerror={(error) => latestError = error} onvaultchange={(status) => { vault = status; retentionDays = status.retention_days ?? retentionDays; selectedVaultID = status.vault_id ?? selectedVaultID; }} />
</main>
