<script lang="ts">
  import { getAccountStatus, unlinkAccount, type AccountStatus } from '../../lib/api/account';
  import type { TalosError } from '../../lib/api/vault';

  let { vaultState, vaultID, disabled = false, onerror } = $props<{
    vaultState: 'locked' | 'unlocked';
    vaultID: string;
    disabled?: boolean;
    onerror: (error: TalosError) => void;
  }>();
  let account = $state<AccountStatus>({ mode: 'local_only', state: 'vault_locked', link_available: false, reason_code: 'VAULT_NOT_OPEN' });
  let unlinking = $state(false);
  let refreshRevision = 0;

  $effect(() => {
    const expectedState = vaultState;
    const expectedVaultID = vaultID;
    const revision = ++refreshRevision;
    void refresh(expectedState, expectedVaultID, revision);
  });

  async function refresh(expectedState: string, expectedVaultID: string, revision: number) {
    try {
      const result = await getAccountStatus();
      if (revision !== refreshRevision || expectedState !== vaultState || expectedVaultID !== vaultID) return;
      if (result.error) onerror(result.error);
      else if (result.account) account = result.account;
    } catch {
      if (revision === refreshRevision) onerror(localError('ACCOUNT_STATUS_UNAVAILABLE', '계정 연결 상태를 불러오지 못했습니다.'));
    }
  }

  async function handleUnlink() {
    if (account.state !== 'linked' || !account.revision) return;
    unlinking = true;
    try {
      const result = await unlinkAccount(account.revision);
      if (result.error) onerror(result.error);
      else if (result.account) account = result.account;
    } catch {
      onerror(localError('ACCOUNT_UNLINK_FAILED', '계정 연결을 해제하지 못했습니다.'));
    } finally {
      unlinking = false;
    }
  }

  function localError(code: string, message: string): TalosError {
    return { code, message, retryable: false, correlation_id: '' };
  }
</script>

<section class="boundary" aria-labelledby="boundary-title">
  <div>
    <p class="eyebrow">CURRENT BOUNDARY</p>
    <h2 id="boundary-title">계정 연결과 데이터 공유는 별개입니다</h2>
  </div>
  <div class="boundary-account" aria-live="polite">
    <strong>{account.state === 'linked' ? 'ZDP 계정 연결됨' : account.state === 'vault_locked' ? 'Vault 잠김' : '로컬 전용'}</strong>
    <p>
      {account.state === 'linked'
        ? '이 Vault의 계정 참조만 연결돼 있습니다. 저장소와 기억은 자동으로 공유되지 않습니다.'
        : account.state === 'vault_locked'
          ? 'Vault를 열면 이 기기의 계정 연결 상태를 확인할 수 있습니다.'
          : '계정 연결이 정식으로 열리기 전까지 모든 작업은 이 기기에만 남습니다.'}
    </p>
    {#if account.state === 'linked'}
      <button type="button" onclick={handleUnlink} disabled={disabled || unlinking}>연결 해제</button>
    {/if}
  </div>
</section>
