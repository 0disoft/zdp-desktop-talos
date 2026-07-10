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
    <span class="phase">Architecture Spine</span>
  </header>

  <section class="hero" aria-labelledby="hero-title">
    <p class="eyebrow">LOCAL DEVELOPMENT PARTNER</p>
    <h1 id="hero-title">다음 작업까지 이어지는 기억</h1>
    <p class="lede">
      작업 계약, 사용자 결정, 패치와 검증 증거를 로컬에 남기고 필요한 순간에만 다시 사용합니다.
    </p>
  </section>

  <section class="status-grid" aria-label="Talos 상태">
    <article class="status-card">
      <div class="status-heading">
        <span class="status-dot locked" aria-hidden="true"></span>
        <h2>Vault</h2>
      </div>
      <strong>잠김</strong>
      <p>영구 키 저장소 연결 전에는 로컬 데이터를 만들지 않습니다.</p>
    </article>

    <article class="status-card">
      <div class="status-heading">
        <span class="status-dot waiting" aria-hidden="true"></span>
        <h2>Worker</h2>
      </div>
      <strong>대기</strong>
      <p>프로토콜 버전 1의 handshake와 안전한 종료 경로가 준비되어 있습니다.</p>
    </article>

    <article class="status-card">
      <div class="status-heading">
        <span class="status-dot clear" aria-hidden="true"></span>
        <h2>최근 오류</h2>
      </div>
      <strong>없음</strong>
      <p>오류 상세 대신 안전한 코드와 상관관계 ID만 화면에 표시합니다.</p>
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
