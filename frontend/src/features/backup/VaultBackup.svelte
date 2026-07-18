<script lang="ts">
  import {
    createVaultBackup,
    preflightVaultBackup,
    type TalosError,
    type VaultBackupPreflight,
    type VaultBackupReceipt,
  } from '../../lib/api/vault';

  let { vaultState, vaultID, disabled = false, onerror } = $props<{
    vaultState: 'locked' | 'unlocked';
    vaultID: string;
    disabled?: boolean;
    onerror: (error: TalosError) => void;
  }>();

  let destination = $state('');
  let source = $state('');
  let busy = $state(false);
  let receipt = $state<VaultBackupReceipt | null>(null);
  let preflight = $state<VaultBackupPreflight | null>(null);
  let scopeKey = $state('');

  $effect(() => {
    const current = `${vaultState}:${vaultID}`;
    if (current === scopeKey) return;
    scopeKey = current;
    destination = '';
    source = '';
    receipt = null;
    preflight = null;
  });

  async function createBackup() {
    const expectedVaultID = vaultID;
    if (vaultState !== 'unlocked' || !expectedVaultID || !destination.trim()) return;
    busy = true;
    receipt = null;
    try {
      const result = await createVaultBackup(destination.trim());
      if (vaultState !== 'unlocked' || vaultID !== expectedVaultID) return;
      if (result.error) onerror(result.error);
      else if (result.receipt) {
        receipt = result.receipt;
        source = result.receipt.path;
      }
    } catch {
      if (vaultState === 'unlocked' && vaultID === expectedVaultID) {
        onerror(localError('VAULT_BACKUP_REQUEST_FAILED', '암호화 백업을 만들지 못했습니다.'));
      }
    } finally {
      busy = false;
    }
  }

  async function runPreflight() {
    const expectedVaultID = vaultID;
    if (vaultState !== 'unlocked' || !expectedVaultID || !source.trim()) return;
    busy = true;
    preflight = null;
    try {
      const result = await preflightVaultBackup(source.trim());
      if (vaultState !== 'unlocked' || vaultID !== expectedVaultID) return;
      if (result.error) onerror(result.error);
      else if (result.preflight) preflight = result.preflight;
    } catch {
      if (vaultState === 'unlocked' && vaultID === expectedVaultID) {
        onerror(localError('VAULT_BACKUP_PREFLIGHT_FAILED', '백업 복원 리허설을 완료하지 못했습니다.'));
      }
    } finally {
      busy = false;
    }
  }

  function localError(code: string, message: string): TalosError {
    return { code, message, retryable: false, correlation_id: '' };
  }

  function size(value: number): string {
    if (value < 1024) return `${value} B`;
    if (value < 1024 * 1024) return `${(value / 1024).toFixed(1)} KiB`;
    if (value < 1024 * 1024 * 1024) return `${(value / (1024 * 1024)).toFixed(1)} MiB`;
    return `${(value / (1024 * 1024 * 1024)).toFixed(2)} GiB`;
  }
</script>

<section class="backup-shell" aria-labelledby="backup-title">
  <div class="backup-header">
    <div>
      <span class="eyebrow">RECOVERY</span>
      <h2 id="backup-title">암호화 백업</h2>
      <p>키를 파일에 넣지 않고 현재 Vault의 데이터와 blob을 함께 보관합니다.</p>
    </div>
  </div>

  {#if vaultState !== 'unlocked'}
    <p class="empty">Vault를 열면 백업을 만들거나 복원 리허설을 실행할 수 있습니다.</p>
  {:else}
    <div class="backup-grid">
      <article>
        <h3>새 백업</h3>
        <label>
          <span>저장 파일</span>
          <input bind:value={destination} maxlength="32767" placeholder="C:\Backups\talos.talos-backup" disabled={disabled || busy} />
        </label>
        <button type="button" onclick={createBackup} disabled={disabled || busy || !destination.trim()}>백업 만들기</button>
        <small>기존 파일은 덮어쓰지 않습니다.</small>
        {#if receipt}
          <div class="receipt">
            <strong>백업 검증 완료</strong>
            <span>{size(receipt.encrypted_size_bytes)} · 이벤트 {receipt.event_count} · blob {receipt.artifact_count}</span>
            <code>{receipt.ciphertext_sha256}</code>
          </div>
        {/if}
      </article>

      <article>
        <h3>복원 리허설</h3>
        <label>
          <span>백업 파일</span>
          <input bind:value={source} maxlength="32767" placeholder="C:\Backups\talos.talos-backup" disabled={disabled || busy} />
        </label>
        <button type="button" class="secondary" onclick={runPreflight} disabled={disabled || busy || !source.trim()}>별도 위치에서 검증</button>
        <small>현재 Vault를 바꾸지 않고 암호 해제, 참조, migration을 검사합니다.</small>
        {#if preflight}
          <div class="receipt">
            <strong>{preflight.migration_required ? 'Migration 리허설 통과' : '현재 schema로 복원 가능'}</strong>
            <span>schema {preflight.source_schema_version} → {preflight.target_schema_version} · 이벤트 {preflight.event_count} · blob {preflight.artifact_count}</span>
            <time>{new Date(preflight.verified_at).toLocaleString('ko-KR')}</time>
          </div>
        {/if}
      </article>
    </div>
    <p class="notice">이 백업은 같은 Vault 키를 복구할 수 있는 Windows 사용자에서만 열 수 있습니다. 다른 기기 복구는 기기 가입 절차를 사용하세요.</p>
  {/if}
</section>

<style>
  .backup-shell{margin-top:1rem;padding:1.15rem;border:1px solid #34412b;border-radius:18px;background:linear-gradient(145deg,#151b12,#0b100a);color:#e8eef7}.backup-header h2,.backup-header p{margin:0}.backup-header p{margin-top:.25rem;color:#9eae95}.eyebrow{font-size:.7rem;font-weight:800;letter-spacing:.14em;color:#b9f34a}.backup-grid{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:.85rem;margin-top:1rem}.backup-grid article{border:1px solid #34412b;border-radius:14px;background:#0e140c;padding:.9rem}.backup-grid h3{margin:0 0 .75rem}.backup-grid label{display:grid;gap:.35rem;margin:.6rem 0;color:#a5b59c;font-size:.78rem}.backup-grid input{width:100%;box-sizing:border-box;border:1px solid #46553d;border-radius:9px;background:#091008;color:#f3f6fa;padding:.62rem;font:inherit}.backup-shell button{border:0;border-radius:9px;background:#c6f35b;color:#12180a;padding:.6rem .8rem;font-weight:800;cursor:pointer}.backup-shell button.secondary{background:#1c2818;color:#dce6d7;border:1px solid #46553d}.backup-shell button:disabled{opacity:.45;cursor:not-allowed}.backup-grid small{display:block;margin-top:.55rem;color:#83927c}.receipt{display:grid;gap:.3rem;margin-top:.75rem;padding:.7rem;border:1px solid #566a45;border-radius:10px;background:#172012}.receipt strong{color:#c6f35b}.receipt span,.receipt time{font-size:.78rem;color:#a9bba0}.receipt code{font-size:.68rem;color:#7f9277;overflow-wrap:anywhere}.notice,.empty{margin:.9rem 0 0;color:#8fa087;font-size:.8rem}@media(max-width:860px){.backup-grid{grid-template-columns:1fr}}
</style>
