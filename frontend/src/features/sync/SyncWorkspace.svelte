<script lang="ts">
  import {
    acceptEnrollmentOffer,
    cancelEnrollment,
    completeEnrollment,
    createEnrollmentOffer,
    exportSyncFolder,
    exportSyncGit,
    getSyncOverview,
    importSyncFolder,
    importSyncGit,
    initializeSync,
    mapSyncWorkspace,
    revokeSyncDevice,
    revokeSyncWorkspace,
    type AcceptanceResult,
    type FolderResult,
    type OfferResult,
    type SyncOverview,
  } from '../../lib/api/sync';
  import type { TalosError, VaultStatus } from '../../lib/api/vault';

  let { vaultState, vaultID, disabled = false, onerror, onvaultchange } = $props<{ vaultState: 'locked' | 'unlocked'; vaultID: string; disabled?: boolean; onerror: (error: TalosError) => void; onvaultchange: (vault: VaultStatus) => void }>();
  let overview = $state<SyncOverview | null>(null);
  let busy = $state(false);
  let validMinutes = $state(60);
  let offer = $state<OfferResult | null>(null);
  let incomingOffer = $state('');
  let incomingSecret = $state('');
  let acceptance = $state<AcceptanceResult | null>(null);
  let completionAcceptance = $state('');
  let completionSecret = $state('');
  let folderRoot = $state('');
  let importDeviceID = $state('');
  let folderResult = $state<FolderResult | null>(null);
  let gitRoot = $state('');
  let gitResult = $state<FolderResult | null>(null);
  let workspacePaths = $state<Record<string, string>>({});
  let copied = $state('');
  let refreshKey = $state('');

  $effect(() => {
    const key = `${vaultState}:${vaultID}`;
    if (key === refreshKey) return;
    refreshKey = key;
    const preserveAcceptance = vaultState === 'unlocked' && vaultID !== '' && acceptance?.vault?.vault_id === vaultID;
    offer = null;
    if (!preserveAcceptance) acceptance = null;
    incomingOffer = '';
    incomingSecret = '';
    completionAcceptance = '';
    completionSecret = '';
    folderRoot = '';
    importDeviceID = '';
    folderResult = null;
    gitRoot = '';
    gitResult = null;
    workspacePaths = {};
    if (vaultState === 'unlocked' && vaultID) void refresh();
    else overview = null;
  });

  async function refresh() {
    if (vaultState !== 'unlocked') return;
    const requestedVaultID = vaultID;
    busy = true;
    try {
      const result = await getSyncOverview();
      if (vaultState !== 'unlocked' || vaultID !== requestedVaultID) return;
      if (result.error) { overview = null; onerror(result.error); }
      else overview = result.overview ?? null;
      if (!importDeviceID) importDeviceID = result.overview?.devices.find((item) => !item.local && item.state === 'active')?.device_id ?? '';
    } catch { if (vaultState === 'unlocked' && vaultID === requestedVaultID) { overview = null; onerror(localError('SYNC_STATUS_UNAVAILABLE', '동기화 상태를 불러오지 못했습니다.')); } }
    finally { busy = false; }
  }

  async function createOffer() {
    busy = true; offer = null;
    try { const result = await createEnrollmentOffer(validMinutes); if (result.error) onerror(result.error); else { offer = result; await refresh(); } }
    catch { onerror(localError('SYNC_ENROLLMENT_FAILED', '가입 패키지를 만들지 못했습니다.')); }
    finally { busy = false; }
  }

  async function initialize() { await mutate(initializeSync); }

  async function acceptOffer() {
    busy = true; acceptance = null;
    try {
      const result = await acceptEnrollmentOffer(incomingOffer.trim(), incomingSecret.trim());
      if (result.error) onerror(result.error);
      else { acceptance = result; if (result.vault) onvaultchange(result.vault); incomingOffer = ''; incomingSecret = ''; }
    } catch { onerror(localError('SYNC_ENROLLMENT_FAILED', '가입 패키지를 받지 못했습니다.')); }
    finally { busy = false; }
  }

  async function completeOffer() {
    await mutate(() => completeEnrollment(completionAcceptance.trim(), completionSecret.trim()));
    completionAcceptance = ''; completionSecret = '';
  }

  async function cancel(id: string) { await mutate(() => cancelEnrollment(id)); }
  async function revokeDevice(id: string, revision: number) { await mutate(() => revokeSyncDevice(id, revision)); }
  async function mapWorkspace(taskID: string, revision: number) { const path = workspacePaths[taskID]?.trim(); if (path) await mutate(() => mapSyncWorkspace(taskID, path, revision)); }
  async function revokeWorkspace(id: string, revision: number) { await mutate(() => revokeSyncWorkspace(id, revision)); }

  async function exportFolder() {
    busy = true; folderResult = null;
    try { const result = await exportSyncFolder(folderRoot.trim()); if (result.error) onerror(result.error); else { folderResult = result; await refresh(); } }
    catch { onerror(localError('SYNC_FOLDER_FAILED', '동기화 파일을 내보내지 못했습니다.')); }
    finally { busy = false; }
  }

  async function importFolder() {
    busy = true; folderResult = null;
    try { const result = await importSyncFolder(folderRoot.trim(), importDeviceID); if (result.error) onerror(result.error); else { folderResult = result; await refresh(); } }
    catch { onerror(localError('SYNC_FOLDER_FAILED', '동기화 파일을 가져오지 못했습니다.')); }
    finally { busy = false; }
  }

  async function exportGit() {
    busy = true; gitResult = null;
    try { const result = await exportSyncGit(gitRoot.trim()); if (result.error) onerror(result.error); else { gitResult = result; await refresh(); } }
    catch { onerror(localError('SYNC_GIT_FAILED', 'Git 교환 파일을 준비하지 못했습니다.')); }
    finally { busy = false; }
  }

  async function importGit() {
    busy = true; gitResult = null;
    try { const result = await importSyncGit(gitRoot.trim(), importDeviceID); if (result.error) onerror(result.error); else { gitResult = result; await refresh(); } }
    catch { onerror(localError('SYNC_GIT_FAILED', 'Git 교환 파일을 가져오지 못했습니다.')); }
    finally { busy = false; }
  }

  async function mutate(action: () => Promise<{ ok: boolean; error?: TalosError }>) {
    busy = true;
    try { const result = await action(); if (result.error) onerror(result.error); else await refresh(); }
    catch { onerror(localError('SYNC_REQUEST_FAILED', '동기화 요청 상태를 확인하지 못했습니다.')); }
    finally { busy = false; }
  }

  async function copy(label: string, value: string) {
    try { await navigator.clipboard.writeText(value); copied = label; window.setTimeout(() => { if (copied === label) copied = ''; }, 1800); }
    catch { onerror(localError('CLIPBOARD_WRITE_FAILED', '클립보드에 복사하지 못했습니다.')); }
  }

  function localError(code: string, message: string): TalosError { return { code, message, retryable: false, correlation_id: '' }; }
  function short(value: string, size = 12) { return value.length > size ? `${value.slice(0, size)}…` : value; }
</script>

<section class="sync-shell" aria-labelledby="sync-title">
  <div class="sync-header">
    <div><span class="eyebrow">MULTI-DEVICE</span><h2 id="sync-title">기기 동기화</h2><p>암호화 pack만 교환합니다. 자동 push는 하지 않습니다.</p></div>
    <button type="button" class="secondary" onclick={refresh} disabled={disabled || busy || vaultState !== 'unlocked'}>상태 새로고침</button>
  </div>

  {#if vaultState !== 'unlocked'}
    <div class="locked-enrollment">
      <p class="empty">기존 Vault 상태는 Vault를 연 뒤 확인할 수 있습니다. 새 기기의 가입 패키지는 여기서 받을 수 있습니다.</p>
      <article>
        <h3>가입 패키지 받기</h3>
        <label><span>패키지</span><textarea bind:value={incomingOffer} rows="3" maxlength="131072" disabled={disabled || busy}></textarea></label>
        <label><span>비밀키</span><input type="password" bind:value={incomingSecret} maxlength="256" disabled={disabled || busy} /></label>
        <button type="button" onclick={acceptOffer} disabled={disabled || busy || !incomingOffer.trim() || !incomingSecret.trim()}>새 Vault로 받기</button>
      </article>
    </div>
  {:else}
    {#if overview && !overview.initialized}
      <div class="sync-start">
        <div><strong>이 Vault의 동기화를 시작할까요?</strong><p>이 기기 전용 서명키와 로컬 기기 등록을 만듭니다. 조회만으로는 생성되지 않습니다.</p></div>
        <button type="button" onclick={initialize} disabled={disabled || busy}>동기화 시작</button>
      </div>
    {/if}
    <div class="sync-grid">
      <article>
        <h3>가입 패키지 보내기</h3>
        <label><span>유효 시간</span><select bind:value={validMinutes} disabled={disabled || busy}><option value={15}>15분</option><option value={60}>1시간</option><option value={360}>6시간</option><option value={1440}>24시간</option></select></label>
        <button type="button" onclick={createOffer} disabled={disabled || busy || overview?.initialized !== true}>새 패키지 만들기</button>
        {#if offer?.encoded && offer.secret}
          <div class="secret-box"><strong>{new Date(offer.expires_at ?? '').toLocaleString('ko-KR')}까지</strong><p>패키지와 비밀키를 다른 경로로 전달하세요.</p><button type="button" class="secondary" onclick={() => copy('offer', offer?.encoded ?? '')}>{copied === 'offer' ? '복사됨' : '패키지 복사'}</button><button type="button" class="secondary" onclick={() => copy('secret', offer?.secret ?? '')}>{copied === 'secret' ? '복사됨' : '비밀키 복사'}</button></div>
        {/if}
      </article>

      <article>
        <h3>가입 패키지 받기</h3>
        <label><span>패키지</span><textarea bind:value={incomingOffer} rows="3" maxlength="131072" disabled={disabled || busy || vaultState === 'unlocked'}></textarea></label>
        <label><span>비밀키</span><input type="password" bind:value={incomingSecret} maxlength="256" disabled={disabled || busy || vaultState === 'unlocked'} /></label>
        <button type="button" onclick={acceptOffer} disabled={disabled || busy || vaultState === 'unlocked' || !incomingOffer.trim() || !incomingSecret.trim()}>새 Vault로 받기</button>
        {#if vaultState === 'unlocked'}<small>현재 Vault를 잠근 뒤 받을 수 있습니다.</small>{/if}
        {#if acceptance?.encoded_acceptance}
          <div class="secret-box"><strong>응답 패키지 준비됨</strong><button type="button" class="secondary" onclick={() => copy('acceptance', acceptance?.encoded_acceptance ?? '')}>{copied === 'acceptance' ? '복사됨' : '응답 복사'}</button></div>
        {/if}
      </article>

      <article>
        <h3>가입 완료</h3>
        <label><span>응답 패키지</span><textarea bind:value={completionAcceptance} rows="3" maxlength="131072" disabled={disabled || busy}></textarea></label>
        <label><span>원래 비밀키</span><input type="password" bind:value={completionSecret} maxlength="256" disabled={disabled || busy} /></label>
        <button type="button" onclick={completeOffer} disabled={disabled || busy || overview?.initialized !== true || !completionAcceptance.trim() || !completionSecret.trim()}>기기 등록 완료</button>
      </article>

      <article>
        <h3>폴더 교환</h3>
        <label><span>교환 폴더</span><input bind:value={folderRoot} maxlength="32767" placeholder="C:\TalosSync" disabled={disabled || busy} /></label>
        <label><span>보낸 기기</span><select bind:value={importDeviceID} disabled={disabled || busy}><option value="">선택</option>{#each overview?.devices.filter((item) => !item.local && item.state === 'active') ?? [] as item}<option value={item.device_id}>{short(item.device_id, 20)}</option>{/each}</select></label>
        <div class="actions"><button type="button" onclick={exportFolder} disabled={disabled || busy || overview?.initialized !== true || !folderRoot.trim()}>내보내기</button><button type="button" class="secondary" onclick={importFolder} disabled={disabled || busy || overview?.initialized !== true || !folderRoot.trim() || !importDeviceID}>가져오기</button></div>
        {#if folderResult}<p class="receipt">{folderResult.relative_path ? `${folderResult.sequence_start}–${folderResult.sequence_end} · ${folderResult.file_replay ? '기존 파일' : '새 파일'}` : `${folderResult.imported}개 pack · 적용 ${folderResult.applied} · 충돌 ${folderResult.conflicted} · 격리 ${folderResult.quarantined}`}</p>{/if}
      </article>

      <article>
        <h3>Git 교환</h3>
        <p class="notice">깨끗한 브랜치에서만 동작합니다. Talos는 commit, pull, push를 실행하지 않습니다.</p>
        <label><span>Git 저장소 루트</span><input bind:value={gitRoot} maxlength="32767" placeholder="C:\TalosSyncRepository" disabled={disabled || busy} /></label>
        <label><span>보낸 기기</span><select bind:value={importDeviceID} disabled={disabled || busy}><option value="">선택</option>{#each overview?.devices.filter((item) => !item.local && item.state === 'active') ?? [] as item}<option value={item.device_id}>{short(item.device_id, 20)}</option>{/each}</select></label>
        <div class="actions"><button type="button" onclick={exportGit} disabled={disabled || busy || overview?.initialized !== true || !gitRoot.trim()}>pack 준비</button><button type="button" class="secondary" onclick={importGit} disabled={disabled || busy || overview?.initialized !== true || !gitRoot.trim() || !importDeviceID}>커밋에서 가져오기</button></div>
        {#if gitResult}<p class="receipt">{gitResult.relative_path ? `${gitResult.relative_path} · 직접 commit/push 필요` : `${gitResult.imported}개 pack · 적용 ${gitResult.applied} · 충돌 ${gitResult.conflicted} · 격리 ${gitResult.quarantined}`}</p>{/if}
      </article>
    </div>

    <div class="sync-list-block">
      <div class="block-title"><h3>기기</h3><span>{overview?.devices.length ?? 0}</span></div>
      <div class="compact-list">{#each overview?.devices ?? [] as item (item.device_id)}<section><div><strong>{item.local ? '이 기기' : short(item.device_id, 24)}</strong><small>{item.state} · rev {item.revision} · 다음 #{item.next_sequence}</small><code>{short(item.key_fingerprint, 18)}</code></div>{#if !item.local && item.state === 'active'}<button type="button" class="danger" onclick={() => revokeDevice(item.device_id, item.revision)} disabled={disabled || busy}>폐기</button>{/if}</section>{:else}<p class="empty">등록된 기기가 없습니다.</p>{/each}</div>
    </div>

    <div class="sync-list-block">
      <div class="block-title"><h3>가입 기록</h3><span>{overview?.enrollments.length ?? 0}</span></div>
      <div class="compact-list">{#each overview?.enrollments ?? [] as item (item.enrollment_id)}<section><div><strong>{item.role === 'issuer' ? '보낸 요청' : '받은 요청'}</strong><small>{item.state} · {new Date(item.expires_at).toLocaleString('ko-KR')}</small><code>{short(item.enrollment_id, 22)}</code></div>{#if item.state === 'offered' || item.state === 'accepted'}<button type="button" class="danger" onclick={() => cancel(item.enrollment_id)} disabled={disabled || busy}>취소</button>{/if}</section>{:else}<p class="empty">가입 기록이 없습니다.</p>{/each}</div>
    </div>

    <div class="sync-list-block wide">
      <div class="block-title"><h3>저장소 연결</h3><span>{overview?.workspaces.length ?? 0}</span></div>
      <div class="workspace-list">{#each overview?.workspaces ?? [] as item (item.task_id)}<section><div><strong>{short(item.task_id, 24)}</strong><small>{item.mapped ? item.local_root : '이 기기에서 저장소 연결 필요'}</small><code>{short(item.baseline_commit, 16)}</code></div><label><span>로컬 Git 폴더</span><input bind:value={workspacePaths[item.task_id]} maxlength="32767" placeholder={item.local_root ?? 'C:\workspace\repository'} disabled={disabled || busy} /></label><div class="actions"><button type="button" onclick={() => mapWorkspace(item.task_id, item.revision ?? 0)} disabled={disabled || busy || !workspacePaths[item.task_id]?.trim()}>{item.mapped ? '다시 연결' : '연결'}</button>{#if item.mapped && item.revision}<button type="button" class="danger" onclick={() => revokeWorkspace(item.workspace_id, item.revision ?? 0)} disabled={disabled || busy}>연결 해제</button>{/if}</div></section>{:else}<p class="empty">동기화된 Task가 없습니다.</p>{/each}</div>
    </div>

    <div class="sync-list-block wide">
      <div class="block-title"><h3>가져오기 기록</h3><span>{overview?.replays.length ?? 0}</span></div>
      <div class="compact-list">{#each overview?.replays ?? [] as item (item.pack_id)}<section class:attention={item.conflicted_count > 0 || item.quarantined_count > 0}><div><strong>#{item.sequence_start}–{item.sequence_end}</strong><small>적용 {item.applied_count} · 충돌 {item.conflicted_count} · 격리 {item.quarantined_count}</small>{#if item.reason_codes.length}<p>{item.reason_codes.join(', ')}</p>{/if}</div><time>{new Date(item.completed_at).toLocaleString('ko-KR')}</time></section>{:else}<p class="empty">가져온 pack이 없습니다.</p>{/each}</div>
    </div>
  {/if}
</section>

<style>
  .sync-shell{margin-top:1rem;padding:1.15rem;border:1px solid #273548;border-radius:18px;background:linear-gradient(145deg,#111923,#0b1119);color:#e8eef7}.sync-header,.block-title,.actions,.sync-start{display:flex;align-items:center;justify-content:space-between;gap:.75rem}.sync-header h2,.sync-header p,.sync-list-block h3,.sync-start p{margin:0}.sync-header p,.sync-start p{margin-top:.25rem;color:#91a3b8}.sync-start{margin-top:1rem;padding:.85rem;border:1px solid #52642b;border-radius:12px;background:#141b0d}.eyebrow{font-size:.7rem;font-weight:800;letter-spacing:.14em;color:#b9f34a}.sync-grid{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:.85rem;margin-top:1rem}.sync-grid article,.sync-list-block,.locked-enrollment article{border:1px solid #243244;border-radius:14px;background:#0d141d;padding:.9rem}.sync-grid h3,.locked-enrollment h3{margin:0 0 .75rem}.notice{margin:.25rem 0;color:#91a3b8;font-size:.78rem}.sync-grid label,.workspace-list label,.locked-enrollment label{display:grid;gap:.35rem;margin:.6rem 0;color:#9fb0c4;font-size:.78rem}.sync-grid input,.sync-grid textarea,.sync-grid select,.workspace-list input,.locked-enrollment input,.locked-enrollment textarea{width:100%;box-sizing:border-box;border:1px solid #34455b;border-radius:9px;background:#091018;color:#f3f6fa;padding:.62rem;font:inherit}.sync-grid textarea,.locked-enrollment textarea{resize:vertical}.locked-enrollment{max-width:42rem}.sync-shell button{border:0;border-radius:9px;background:#c6f35b;color:#12180a;padding:.6rem .8rem;font-weight:800;cursor:pointer}.sync-shell button.secondary{background:#1a2634;color:#dce6f2;border:1px solid #34455b}.sync-shell button.danger{background:#3a1820;color:#ffb8c3;border:1px solid #783141}.sync-shell button:disabled{opacity:.45;cursor:not-allowed}.secret-box{margin-top:.7rem;padding:.7rem;border:1px solid #6b5c24;border-radius:10px;background:#211d0e}.secret-box p{color:#d3c58c;font-size:.78rem}.secret-box button{margin-right:.4rem}.receipt{font-size:.8rem;color:#b9f34a;overflow-wrap:anywhere}.sync-list-block{margin-top:.85rem}.block-title span{min-width:1.6rem;text-align:center;border-radius:999px;background:#1d2a38;color:#c7d4e2;font-size:.75rem;padding:.2rem .45rem}.compact-list,.workspace-list{display:grid;gap:.55rem;margin-top:.7rem}.compact-list section,.workspace-list section{display:flex;align-items:center;justify-content:space-between;gap:.8rem;border-top:1px solid #202d3d;padding-top:.6rem}.compact-list section:first-child,.workspace-list section:first-child{border-top:0}.compact-list div,.workspace-list div{display:grid;gap:.2rem;min-width:0}.compact-list small,.workspace-list small{color:#91a3b8}.compact-list code,.workspace-list code{color:#73869d;font-size:.72rem;overflow-wrap:anywhere}.compact-list time{font-size:.72rem;color:#8294a8}.compact-list .attention{border-color:#713646;background:#24131a;padding:.6rem;border-radius:10px}.compact-list p{margin:.15rem 0;color:#ffb8c3;font-size:.75rem}.workspace-list section{display:grid;grid-template-columns:minmax(0,1.2fr) minmax(15rem,1fr) auto}.empty{margin:.8rem 0;color:#8799ad}.wide{grid-column:1/-1}@media(max-width:860px){.sync-grid{grid-template-columns:1fr}.workspace-list section{grid-template-columns:1fr}.sync-header,.sync-start{align-items:flex-start;flex-direction:column}}
</style>
