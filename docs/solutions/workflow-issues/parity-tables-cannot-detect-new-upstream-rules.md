---
title: 패리티 테이블은 upstream이 새로 추가한 필터 규칙을 감지하지 못한다
date: 2026-09-23
category: workflow-issues
module: upstream-sync
problem_type: workflow_issue
component: development_workflow
severity: high
related_components:
  - testing_framework
  - tooling
  - documentation
applies_when:
  - "브랜치 전용 코드가 upstream 로직의 복제본(필터·선택·정규화 등)을 갖고 있고 패리티 테스트로만 일치를 보증할 때"
  - "upstream 태그 병합 후 build·vet·전체 테스트가 모두 통과해 병합이 안전하다고 판단하려 할 때"
  - "upstream이 `whyExcluded`·`selectFiles`나 allowlist JSON(기본 제외·secret 패턴)을 바꾼 릴리스를 병합할 때"
  - "upstream 기본값 변경이 기존 테스트 케이스의 전제(특정 경로가 기본 필터에 안 걸린다 등)를 바꿀 수 있을 때"
  - "충돌을 한쪽으로 통째로 풀었거나 과거 병합이 upstream이 삭제한 코드를 되살렸을 가능성이 있을 때"
symptoms:
  - "v1.12.9 병합 후 build·vet·`go test -race ./...`가 모두 통과했지만 `coreFilterReason`에는 secret 경로 게이트가 없어 `.env`가 리뷰 대상이 될 상태였음"
  - "upstream #1262/#1279가 `whyExcluded`에 secret 경로 게이트를 추가했으나 브랜치 전용 파일이라 텍스트 충돌이 없었음"
  - "기존 패리티 테이블은 통과했고 양쪽 테이블에 secret 케이스를 새로 넣은 뒤에야 core 쪽만 6건 실패"
  - "upstream #1499가 `vendor/`를 기본 제외에 넣어 기존 패리티 케이스가 양쪽 모두 실패"
  - "v1.11.2 병합이 #808에서 삭제된 미사용 메서드 `executePlanPhase`를 되살렸으나 컴파일·vet·테스트 어느 것도 잡지 못함"
root_cause: missing_workflow_step
resolution_type: workflow_improvement
tags: [upstream-sync, parity-test, branch-only-code, file-filter, secret-path, silent-drift, dead-code-resurrection, ocr-core]
---

# 패리티 테이블은 upstream이 새로 추가한 필터 규칙을 감지하지 못한다

## Context

`feat/ocr-core-local`은 upstream(alibaba/open-code-review)에 없는 `ocr core` 명령 그룹을 가진 장수 브랜치다. 이 브랜치에는 upstream 필터 알고리즘의 **복제본**이 있다. `ocr core diff`가 쓰는 `coreFilterReason`(`internal/diff/core_diff.go:175`)이다. 이 복제본은 `ocr review`의 파일 선택(`internal/agent/selection.go:70`의 `whyExcluded`)과 같은 결과를 내야 한다.

두 함수의 일치는 컴파일러가 보증하지 않는다. 대신 같은 케이스를 담은 **패리티 테이블 두 개**가 보증한다.

- `internal/diff/core_filter_parity_test.go` — core 쪽
- `internal/agent/whyexcluded_parity_test.go` — agent 쪽

2026-09-23에 upstream 태그 `v1.12.9`(v1.11.2 이후 125커밋)를 병합했다. 선행 학습(`clean-merge-does-not-prove-branch-only-files-compile.md`)의 절차를 그대로 따랐다. 스크래치 워크트리에서 시험 병합하고, 충돌 5건을 풀고, `go build`·`go vet`·`go test ./...`를 돌렸다. **모두 통과했다.** 그런데 `coreFilterReason`에는 upstream이 새로 넣은 규칙이 빠져 있었다. `ocr core diff`는 `.env` 파일을 리뷰 대상으로 내보낼 상태였다. 이 누락은 테스트가 아니라, 충돌을 풀면서 upstream의 새 `selection.go`를 읽다가 발견했다.

원인은 upstream PR #1262(feat(allowlist): exclude secret paths from review)다. 이 PR은 agent와 scan의 `whyExcluded`에 secret 경로 게이트를 **사용자 규칙보다 앞에** 추가했다(`internal/agent/selection.go:80`, `internal/scan/agent.go:509`). #1279(protect per-environment .env files)가 `.env.*` 계열로 범위를 넓혔다. 판정 함수는 `allowedext.IsSecretPath`(`internal/config/allowlist/secret_path.go:47`)다. (upstream 소스 주석은 "See #1240"이라고 쓰는데, 이것은 이슈 번호로 보인다. 이 브랜치의 병합 커밋 본문도 "#1240"으로 적었다. 기능을 넣은 PR은 #1262다.)

이 변경은 세 겹의 검사를 모두 통과했다.

1. **텍스트 충돌 없음.** `core_diff.go`는 브랜치 전용 파일이라 upstream 쪽 대응물이 없다.
2. **컴파일 오류 없음.** 새 게이트는 분기 하나를 **추가**했을 뿐이고, 기존 시그니처는 그대로다.
3. **테스트 실패 없음.** 두 패리티 테이블 모두 secret 케이스를 **하나도 몰랐다.** 없는 케이스는 실패하지 않는다.

양쪽 테이블에 secret 케이스 6개를 넣자 core 쪽만 6건 실패했다(RED). 그 뒤 `coreFilterReason`에 같은 게이트를 넣어 통과시켰다(`internal/diff/core_diff.go:184`).

같은 병합에서 비슷한 종류의 문제가 두 건 더 나왔다.

- **기존 케이스의 전제가 깨짐.** upstream #1499(exclude dependency and build-output directories by default)가 `**/vendor/**`를 기본 제외 목록에 넣었다(`internal/config/allowlist/default_exclude_patterns.json:79`). 패리티 케이스 "nested path needs a trailing doublestar"는 `vendor/x/y/pkg.go`가 기본 필터에 걸리지 **않는다는** 가정 위에 있었다. 이번에는 양쪽 테이블이 함께 실패했다. 테스트 의도(단일 `*`는 하위 디렉터리와 맞지 않음)는 맞았고, 예시 경로의 전제만 틀렸다.
- **죽은 코드 부활.** `(*Agent).executePlanPhase`는 upstream #808(파일 그룹 리뷰)이 지운 메서드다. 그런데 브랜치의 `internal/agent/agent.go`에는 남아 있었다. v1.11.2 병합 직전 브랜치에는 있었고 v1.11.2 태그에는 없었다. 이 세션의 결론은 이렇다. 이 메서드가 브랜치가 지운 `extFromPath`와 같은 충돌 헝크에 있었고, 그 헝크를 브랜치 쪽으로 풀면서 함께 살아남았다. 호출처는 0이었다. Go 컴파일러와 `go vet`은 쓰이지 않는 **메서드**를 보고하지 않으므로 아무 검사도 잡지 못했다.

## Guidance

선행 학습은 "시험 병합 → 빌드·vet·테스트 통과"를 병합 안전의 확인 지점으로 둔다. 브랜치에 upstream 로직의 **복제본**이 있으면 그 뒤에 한 단계가 더 필요하다. 이번 학습의 핵심은 이것이다. **패리티 테이블은 이미 있는 케이스만 지킨다. upstream이 새 규칙을 추가하면 두 테이블 모두 그 규칙을 모르므로 초록불이 유지된다.**

### 1. upstream 원본 함수와 규칙 데이터의 diff를 직접 읽는다

충돌 목록과 테스트 결과에 기대지 않는다. 복제본의 원본이 되는 upstream 코드를 태그 사이에서 직접 비교한다.

```bash
# 필터 원본: agent(review)와 scan
git diff v1.11.2 v1.12.9 -- internal/agent/preview.go internal/agent/selection.go internal/scan/agent.go

# 필터가 읽는 규칙 데이터
git diff v1.11.2 v1.12.9 -- internal/config/allowlist/

# diff provider 단계의 제외(복제본이 GetDiff를 쓰면 자동 상속, 아니면 따로 반영)
git diff v1.11.2 v1.12.9 -- internal/diff/git.go | grep -n 'providerDirIgnoreDirs' -A15
```

upstream이 함수를 다른 파일로 옮겼을 수 있다. 이번에는 `whyExcluded`가 `preview.go`에서 새 파일 `selection.go`로 옮겨졌다(#801, `selectFiles` 도입). 옛 경로만 비교하면 "삭제됨"으로만 보인다. `git log -S 'func (a *Agent) whyExcluded' v1.11.2..v1.12.9`로 새 위치를 찾는다.

### 2. 새 분기마다 패리티 케이스를 먼저 추가하고 RED를 확인한다

upstream 원본에 새 `if`/`case`가 생겼으면 복제본을 고치기 **전에** 양쪽 테이블에 같은 케이스를 넣는다. 그다음 테스트를 돌려 core 쪽이 실패하는지 본다.

- core 쪽만 실패 → 복제본이 뒤처졌다는 증거다. 복제본을 고친다.
- 양쪽 다 통과 → 케이스가 새 분기를 실제로 타지 않는다. 케이스를 다시 설계한다.
- 양쪽 다 실패 → 케이스의 기대값이나 전제가 틀렸다(아래 3절).

이번 병합에서 넣은 secret 케이스는 새 규칙의 경계를 하나씩 찌른다. 사용자 필터 없음, include가 secret을 되살리려 함, 사용자 exclude가 사유를 바꾸려 함, `.env` → `.env.example` rename(OldPath 쪽 검사), `.env.example` 템플릿(secret 아님), 삭제된 secret 파일(deleted보다 secret이 먼저)이다.

### 3. 테이블 케이스가 기본 목록에 우연히 의존하지 않게 경로를 고른다

케이스 하나는 한 규칙만 시험해야 한다. 예시 경로가 upstream의 **변하는 목록**에 걸리면 그 목록이 바뀔 때 케이스의 의미가 바뀐다. 피해야 할 목록은 세 개다.

- 기본 제외 패턴 — `internal/config/allowlist/default_exclude_patterns.json`
- secret 패턴 — `internal/config/allowlist/default_secret_patterns.json`
- diff provider 디렉터리 — `providerDirIgnoreDirs`(`internal/diff/git.go:28`). `vendor/`(`:33`)뿐 아니라 `pkgs/`(`:40`)도 있다.

이번에는 `vendor/`를 먼저 `pkgs/`로 바꿨다가 `pkgs/`도 provider 목록에 있다는 것을 알고 `apps/`로 다시 바꿨다. `whyExcluded` 수준에서는 provider 목록이 쓰이지 않아 `pkgs/`도 테스트를 통과한다. 하지만 읽는 사람이 "이 경로는 원래 diff에 안 나온다"고 헷갈릴 수 있다. 새 경로를 고를 때는 세 목록을 모두 grep한다.

```bash
grep -n 'apps' internal/config/allowlist/*.json internal/diff/git.go   # 출력이 없어야 한다
```

### 4. 복제본 쪽 테이블은 upstream의 실제 합성 함수를 부른다

agent 쪽 패리티 테스트는 원래 "`whyExcluded` 결과가 None이고 삭제 파일이면 deleted"를 테스트 안에서 **흉내 내어** 합성했다. upstream이 이 합성을 `selectFiles`(`internal/agent/selection.go:43`)로 한곳에 모았으므로, 이제 테스트가 그것을 직접 부른다(`internal/agent/whyexcluded_parity_test.go:186`).

```go
// 이전: 테스트가 합성을 흉내 냄 — upstream 합성이 바뀌어도 모른다
got := a.whyExcluded(tt.diff)
if got == model.ExcludeNone && tt.diff.IsDeleted {
	got = model.ExcludeDeleted
}

// 이후: upstream의 실제 선택 함수를 부른다. zero Template이면 크기 게이트는 꺼진다.
got := a.selectFiles([]model.Diff{tt.diff})[0].Reason
```

테스트가 흉내 낸 합성은 upstream 합성이 바뀌어도 그대로 초록불이다. 실제 함수를 부르면 upstream 쪽 변화가 최소한 agent 테이블에는 전달된다.

### 5. 병합 후 브랜치 델타가 의도한 변경뿐인지 확인한다

컴파일러가 못 잡는 부활 코드를 찾는 방법이다. 병합 결과와 upstream 태그를 비교해서, 브랜치 고유 변경 목록과 대조한다.

```bash
git diff --cached v1.12.9 --stat                      # 브랜치 고유 파일만 남아야 한다
git diff --cached v1.12.9 -- internal/agent/ internal/scan/ | grep '^[-+]' | grep -v '^+++\|^---'
```

upstream 파일에서 나온 `+` 줄이 브랜치가 의도한 정리(공유 primitive 사용, 헬퍼 삭제)가 아니면 부활 코드를 의심한다. 이번에는 충돌을 풀기 전에 `git diff v1.11.2 HEAD -- internal/agent/agent.go`로 브랜치 델타를 보다가, 의도하지 않은 메서드 `executePlanPhase`를 발견했다. 그 뒤 `agent.go`를 upstream 쪽으로 받아 이 메서드를 없앴고, 병합 결과와 `v1.12.9`의 diff에서 사라진 것을 확인했다.

## Why This Matters

패리티 테이블은 **닫힌 목록**이다. 두 구현이 "알려진 입력"에서 같은 답을 낸다는 것만 증명한다. upstream이 새 규칙을 추가하면 그 규칙을 타는 입력은 목록 밖에 있다. 두 테이블은 같은 사람이 같은 시점에 쓴 복사본이라, 둘 다 새 규칙을 모르는 채로 서로 일치한다. 그래서 테스트는 초록불이고 복제본은 틀리다.

이 브랜치에서 upstream 병합이 남긴 침묵 파손은 이번이 세 번째다. 형태가 점점 잡기 어려워졌다.

| 병합 | 파손 | 잡은 도구 |
|---|---|---|
| 2026-08-14 origin/main | `flags.go` 삭제(#625), 심볼 사라짐 | 컴파일러 |
| 2026-09-02 v1.11.2 | `NewResolver` 시그니처 변경(#574), 인자 추가 | 컴파일러 |
| 2026-09-23 v1.12.9 | secret 게이트 추가(#1262), 분기 추가 | **없음** — 새 케이스를 손으로 넣어야 드러남 |

선행 학습의 교훈("충돌 0건 ≠ 컴파일 성공")은 컴파일러를 병합 앞에 세우면 해결됐다. 이번 유형은 컴파일러도 기존 테스트도 원리적으로 볼 수 없다. **사람이 upstream diff를 읽는 단계**만 잡을 수 있다.

결과도 가볍지 않다. 이번 누락은 `.env`, `id_rsa`, `.netrc` 같은 자격증명 파일이 `ocr core diff` 출력에 `will_review: true`로 실려 리뷰 두뇌(Claude Code 구독)로 전송될 수 있는 상태였다. upstream은 바로 이것을 막으려고 게이트를 넣었다. 복제본이 뒤처지면 보안 수정이 브랜치 사용자에게만 적용되지 않는다.

죽은 코드 부활도 같은 뿌리다. Go 컴파일러와 `go vet`은 쓰이지 않는 메서드를 보고하지 않는다. `staticcheck`의 U1000 검사는 쓰이지 않는 미노출 함수·메서드를 보고하지만, 이 저장소의 CI와 `make check`는 `staticcheck`를 돌리지 않는다. 충돌을 한쪽으로 풀 때 그쪽이 가진 코드가 통째로 따라오므로, "빌드된다"는 사실은 "의도한 코드만 있다"는 증거가 아니다.

## When to Apply

- 브랜치에 upstream 로직의 복제본(특히 필터·선택·검증 규칙)이 있고, 그 일치를 패리티 테이블로 보증할 때.
- upstream 태그나 main을 병합할 때, 시험 병합이 빌드·vet·테스트를 모두 통과한 **뒤**. 이 단계를 "추가 확인"이 아니라 병합 절차의 필수 단계로 둔다. 구조적 해법(복제본을 없애고 공유 함수 하나로 합치기, 아래 Related의 R1)을 적용하기 전까지 이 단계가 유일한 방어선이다.
- upstream 릴리스 노트나 커밋 제목에 `allowlist`, `exclude`, `filter`, `selection`, `secret`, `security` 같은 단어가 보일 때. 규칙 추가일 가능성이 높다.
- upstream이 복제본의 원본 함수를 다른 파일로 옮겼을 때. 옮기면서 규칙이 바뀌는 일이 흔하다(#801 → #1262 순서).
- 패리티 테이블의 기존 케이스가 병합 후 **양쪽 모두** 실패할 때. 구현 불일치가 아니라 예시 경로의 전제가 upstream 기본 목록 변경으로 깨졌을 가능성이 크다.
- 충돌을 `--ours`/`--theirs`나 헝크 단위로 한쪽을 택해 풀었을 때. 병합 결과를 upstream 태그와 비교해 부활 코드를 찾는다.

## Examples

### upstream에 새 분기가 생겼는지 찾기

```bash
git diff v1.11.2 v1.12.9 -- internal/agent/selection.go internal/scan/agent.go | grep '^+.*allowedext\.'
# +	if allowedext.IsSecretPath(d.OldPath) || allowedext.IsSecretPath(d.NewPath) {
# +	if allowedext.IsSecretPath(path) {
```

`selection.go`는 이 구간에서 새로 생긴 파일이라 모든 줄이 `+`다. 그래서 `if` 전체가 아니라 규칙 판정 호출(`allowedext.*`)로 좁힌다. 기존에 없던 판정 함수가 보이면 복제본 `coreFilterReason`에 같은 분기가 있는지 확인한다. 이번에는 `IsSecretPath`가 없었다.

### RED 확인: 케이스를 먼저 넣으면 core 쪽만 실패한다

```text
--- FAIL: TestCoreWhyExcluded_FilterParity/secret_path_is_excluded_with_no_filter
    coreWhyExcluded(".env") = "", want "secret_exclude"
--- FAIL: TestCoreWhyExcluded_FilterParity/user_exclude_does_not_reclassify_a_secret_path
    coreWhyExcluded(".env") = "user_exclude", want "secret_exclude"
--- FAIL: TestCoreWhyExcluded_FilterParity/rename_out_of_a_secret_path_stays_excluded
    coreWhyExcluded(".env.example") = "", want "secret_exclude"
--- FAIL: TestCoreWhyExcluded_FilterParity/deleted_secret_file_reports_the_secret_reason
    coreWhyExcluded(".env") = "deleted", want "secret_exclude"
```

agent 쪽 `TestWhyExcluded_CoreDiffParity`는 같은 케이스로 통과했다. 이 비대칭이 "복제본이 뒤처졌다"는 직접 증거다.

### 복제본 수정: upstream 원본을 그대로 따른다

```go
// internal/diff/core_diff.go:182-186
	// Ahead of both user rules, as in internal/agent: no include glob can admit
	// a credential path, and no user exclude can claim it under another reason.
	if allowedext.IsSecretPath(d.OldPath) || allowedext.IsSecretPath(d.NewPath) {
		return model.ExcludeSecret
	}
```

scan 쪽(`internal/scan/agent.go:509`)은 `ScanItem`에 경로가 하나뿐이라 `IsSecretPath(path)`만 본다. `ocr core diff`는 diff를 다루므로 agent 쪽처럼 OldPath와 NewPath를 **둘 다** 본다. 그래야 `.env` → `.env.example` rename이 제외된다. 복제본은 입력 모양이 같은 원본을 따른다.

### 전제가 깨진 케이스의 경로 교체

```go
// 이전: vendor/는 #1499 이후 기본 제외 → 기대값 ExcludeNone이 default_path로 바뀜
diff:   goFile("vendor/x/y/pkg.go"),
filter: &rules.FileFilter{Exclude: []string{"**/vendor/*"}},

// 이후: 어느 기본 목록에도 없는 디렉터리 (internal/agent/whyexcluded_parity_test.go:104)
diff:   goFile("apps/x/y/lib.go"),
filter: &rules.FileFilter{Exclude: []string{"**/apps/*"}},
```

케이스의 의도(단일 `*`는 하위 디렉터리와 맞지 않음, `**`는 맞음)는 그대로다. 짝이 되는 "trailing doublestar excludes the whole directory tree" 케이스도 같은 경로로 함께 바꾼다.


## Related

- `docs/solutions/workflow-issues/clean-merge-does-not-prove-branch-only-files-compile.md` — 직전 단계의 학습. "충돌 0건 ≠ 컴파일 성공"을 다루고 시험 병합 + build·vet·test 절차를 정한다. 이 문서는 그 절차가 통과한 뒤에도 남는 행동 드리프트를 다룬다.
- `docs/residual-review-findings/feat-ocr-core-local-merge-main.md` R1 — 필터 알고리즘이 `internal/diff`·`internal/agent`·`internal/scan`에 세 벌로 복제돼 있다는 지적. "한쪽만 바뀌고 테이블에 케이스가 추가되지 않으면 두 테스트가 통과한 채 선택이 조용히 갈라진다"고 예고했고, v1.12.9 병합이 그 첫 실제 사례다. 권장 해법(공유 `WhyExcluded` 하나로 합치기)이 이 문제의 구조적 예방책이다.
- `docs/solutions/workflow-issues/cross-engine-review-exposes-file-filter-blind-spots.md` — 같은 필터·패리티 테이블을 다룬 선행 학습. 그 문서의 판정 순서 설명에는 secret 게이트가 아직 없다.
- `CONCEPTS.md` — Review parity, Exclude Reason.
- upstream: issue #1240(secret 경로 제외 요청) → PR #1262, issue #1265 → PR #1279, PR #1499(의존성·빌드 산출 디렉터리 기본 제외), issue #782 → PR #801(`selectFiles` 도입), PR #808(`executePlanPhase` 삭제).
