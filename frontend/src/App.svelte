<script lang="ts">
  import { onMount } from 'svelte';
  import {
    createVault,
    getVaultStatus,
    lockVault,
    type TalosError,
    type VaultStatus,
  } from './lib/api/vault';

  let vault = $state<VaultStatus>({ state: 'locked', persistent_key_store: false });
  let retentionDays = $state(30);
  let loading = $state(true);
  let latestError = $state<TalosError | null>(null);

  onMount(async () => {
    try {
      vault = await getVaultStatus();
    } catch {
      latestError = localError('VAULT_STATUS_UNAVAILABLE', 'Vault 상태를 불러오지 못했습니다.');
    } finally {
      loading = false;
    }
  });

  async function handleVaultAction() {
    loading = true;
    latestError = null;
    try {
      const result = vault.state === 'unlocked' ? await lockVault() : await createVault(retentionDays);
      if (result.error) {
        latestError = result.error;
      } else if (result.vault) {
        vault = result.vault;
      }
    } catch {
      latestError = localError('VAULT_REQUEST_FAILED', 'Vault 요청을 완료하지 못했습니다.');
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
        {#if vault.state === 'locked'}
          <label>
            <span>보존 기간</span>
            <select bind:value={retentionDays} disabled={loading}>
              <option value={30}>30일</option>
              <option value={90}>90일</option>
              <option value={365}>365일</option>
            </select>
          </label>
        {/if}
        <button type="button" onclick={handleVaultAction} disabled={loading || (!vault.persistent_key_store && vault.state === 'locked')}>
          {vault.state === 'unlocked' ? 'Vault 잠그기' : '새 Vault 만들기'}
        </button>
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
