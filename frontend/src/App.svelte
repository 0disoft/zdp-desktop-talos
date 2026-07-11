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

  let vault = $state<VaultStatus>({ state: 'locked', persistent_key_store: false });
  let retentionDays = $state(30);
  let loading = $state(true);
  let latestError = $state<TalosError | null>(null);
  let vaults = $state<VaultSummary[]>([]);
  let selectedVaultID = $state('');
  let creatingNew = $state(false);
  let purgeOpen = $state(false);
  let purgeConfirmation = $state('');

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
        }
      } else if (result.vault) {
        vault = result.vault;
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
    } finally {
      loading = false;
    }
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
