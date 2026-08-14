---
title: 리뷰 엔진의 커버리지는 추론 품질이 아니라 파일 필터의 속성이다
date: 2026-08-14
category: workflow-issues
module: code-review-workflow
problem_type: workflow_issue
component: development_workflow
severity: high
related_components:
  - tooling
  - testing_framework
  - documentation
applies_when:
  - "MR 전에 로컬 diff를 코드리뷰 에이전트로 검증할 때"
  - "`ocr core diff`가 파일 선정을 담당하는 리뷰 스킬을 쓸 때"
  - "리뷰 대상에 테스트 파일이나 마크다운 문서 변경이 포함될 때"
  - "리뷰 지적을 수정한 뒤 그 수정 자체를 다시 검증하지 않고 넘어가려 할 때"
  - "여러 리뷰 패스의 발견 수를 엔진 정확도 비교로 해석하려 할 때"
symptoms:
  - "`ocr core diff`가 변경 14개 파일 중 5개만 선정하고 테스트 6개는 default_path, 마크다운 3개는 unsupported_ext로 제외"
  - "제외된 문서 안의 잘못된 glob 예시가 좁은 엔진에는 구조적으로 안 보임"
  - "`runCoreDiff`의 사설 validateDiffMode 복사본을 광범위 엔진의 리뷰어 패스 전체가 놓침"
  - "한 엔진의 지적을 고치다 resolveMaxTokens 음수 검증이 도달 불가가 됨"
  - "두 엔진이 서로 다른 커밋 트리를 봐서 발견 건수 차이를 정확도 비교로 쓸 수 없음"
root_cause: missing_workflow_step
resolution_type: workflow_improvement
tags:
  - code-review
  - cross-validation
  - review-blind-spot
  - file-filter
  - doublestar
  - ocr-core
  - agent-native-review
  - multi-agent-review
---

# 리뷰 엔진의 커버리지는 추론 품질이 아니라 파일 필터의 속성이다

## Context

`feat/ocr-core-local-merge-main` 브랜치는 약 207개 커밋 규모의 `origin/main` 병합에서 빌드를
복구하고 `ocr core` 명령군을 Cobra로 이식한 변경 묶음이다. 이 브랜치를 두 개의 서로 다른
코드리뷰 엔진으로 각각 독립 리뷰했다.

- **`ce-code-review`** — 다중 페르소나 방식. diff 전체를 대상으로 8명의 리뷰어가 붙었고
  (`correctness`, `security`, `adversarial`, `testing`, `api-contract`, `project-standards`,
  `maintainability`, `agent-native`), 그에 앞서 `ce-simplify-code` 3인 패스가 있었다.
  리뷰 구성과 대상 범위(`277bdd6..b62c278` — 모두 **미푸시 로컬 커밋**이므로 다른 체크아웃에는 없고,
  push/rebase 시 SHA가 바뀔 수 있다)는
  `docs/residual-review-findings/feat-ocr-core-local-merge-main.md:3-7`
  에 기록돼 있다.
- **`open-code-review-local`** — 이 저장소 안에 사는 스킬
  (`skills/open-code-review-local/SKILL.md`).
  파일 선정을 `ocr core diff`에 위임하고, 추론만 에이전트가 담당한다. 즉 **`ocr review`의
  프로덕션 파일 필터를 그대로 상속**한다.

두 엔진을 같은 브랜치에 돌려 보니, 각자가 **구조적으로** 볼 수 없는 영역이 서로 달랐다.
그리고 좁은 쪽 엔진의 사각지대는 "추론이 약해서"가 아니라 **파일 필터가 그렇게 정의돼
있어서** 생긴 것이었다. 이 문서는 그 구분을 다음 사람이 스스로 확인할 수 있게 남긴다.

## Guidance

### 1. 리뷰 엔진이 프로덕션 필터에서 파일 집합을 파생한다면, 침묵을 믿기 전에 필터가 무엇을 버렸는지 먼저 열거하라

`ocr core diff`는 매 파일마다 `will_review`와 `exclude_reason`을 함께 내보낸다
(`internal/diff/core_diff.go:49-50`). 리뷰를 시작하기 전에 **제외된 목록을 먼저 읽어라.**

```bash
ocr core diff --repo . --from <base> --to HEAD \
  | jq -r '.files[] | select(.will_review|not) | "\(.exclude_reason)\t\(.path)"' \
  | sort | uniq -c
```

이 한 줄이 "리뷰가 아무것도 못 찾았다"와 "리뷰가 그 파일을 애초에 보지 못했다"를 갈라준다.

### 2. 제외 사유는 6종 + 1이며, 어디서 결정되는지 코드로 확인할 수 있다

공유 상수는 `internal/model/preview.go:11-17`에 있다.

```go
ExcludeNone        ExcludeReason = ""
ExcludeUserRule    ExcludeReason = "user_exclude"
ExcludeExtension   ExcludeReason = "unsupported_ext"
ExcludeDefaultPath ExcludeReason = "default_path"
ExcludeDeleted     ExcludeReason = "deleted"
ExcludeBinary      ExcludeReason = "binary"
```

여기에 core 전용 사유 `large_diff`가 하나 더 붙는다(`internal/diff/core_diff.go:21`).
`large_diff`는 `MaxTokens > 0`일 때 `maxTokens * 4 / 5` 토큰을 넘는 diff 본문에만 적용된다
(`internal/diff/core_diff.go:113-116`, `:137-139`).

판정 순서는 `coreFilterReason`에 그대로 있다(`internal/diff/core_diff.go:175-200`).

1. `d.IsBinary` → `binary` (`:176-178`)
2. 사용자 exclude 매칭 → `user_exclude` (`:182-184`) — **모든 것을 이긴다**
3. 사용자 include가 있고 매칭 → 즉시 통과 (`:186-188`) — 아래 기본 필터를 단락시킨다
4. 확장자 허용목록 밖 → `unsupported_ext` (`:190-193`)
5. 기본 제외 경로 매칭 → `default_path` (`:195-197`)

그리고 위를 통과한 파일에 한해 삭제 여부를 본다 → `deleted`
(`coreWhyExcluded`, `internal/diff/core_diff.go:163-169`).

원시 판정은 `internal/config/allowlist`의 `IsAllowedExt`
(`internal/config/allowlist/allowed_ext.go:77-80`)와 `IsExcludedPath`
(`같은 파일:91-100`)가 담당하며, 후자는 `default_exclude_patterns.json`의 31개 glob을
**소문자화 후** doublestar로 매칭한다(`:66-73`, `:93-95`).

### 3. 리뷰 대상에서 빠지는 대표적 두 부류: 테스트와 문서

- 테스트: `default_exclude_patterns.json`의 첫 패턴이 `**/*_test.go`다. Java/Kotlin/JS/TS/
  Python/Ruby/Rust/Haskell/Nim 테스트 관례 경로, `**/testdata/**`, `**/fixtures/**`,
  `**/__snapshots__/**`, `**/*.pb.go` 등도 함께 들어 있다. → `default_path`
- 문서: `supported_file_types.json`에 `.md`가 **없다**. → `unsupported_ext`

즉 `ocr core diff`를 파일 선정에 쓰는 리뷰는 **테스트 코드와 마크다운 문서의 결함을
구조적으로 볼 수 없다.** 이건 모델 성능 문제가 아니다.

### 4. 사용자 `--exclude` 패턴은 `*`가 `/`를 넘지 않는다 — 문서 예시부터 의심하라

doublestar 문법은 `internal/config/allowlist/allowed_ext.go:12-27`에 명시돼 있다.

```
"*"    matches any character within a single path segment (never crosses /)
"**"   matches zero or more path segments (may cross /)
```

따라서 `**/generated/*`는 `src/generated/api.go`는 걸러도 `src/generated/deep/nested.go`는
**걸러내지 못한다.** 이 동작은 테스트로 못 박혀 있다:

- `internal/diff/core_filter_parity_test.go:107-112` — `"nested path needs a trailing
  doublestar, not a single star"`: `**/vendor/*` + `vendor/x/y/pkg.go` → `model.ExcludeNone`
- 같은 파일 `:113-118` — `**/vendor/**`로 바꾸면 `model.ExcludeUserRule`
- `cmd/opencodereview/core_diff_wiring_test.go:108-141` — CLI 경로에서 동일 사실을 확인

이게 중요한 이유는 `ocr core diff`가 리뷰 대상 파일마다 **`new_file_content` 전문을 JSON으로
그대로 방출**하기 때문이다(`internal/diff/core_diff.go:52`, `:144-148`). 사용자가 믿는 exclude
패턴이 실제로는 under-match하면, 제외했다고 생각한 파일의 내용이 통째로 저장소 밖 컨텍스트로
나간다.

### 5. 넓은 엔진에도 사각지대가 있다: 중복 코드는 "많은 눈"으로 안 잡힌다

이번 세션에서 좁은 엔진(`open-code-review-local`)이 즉시 잡아낸 것은
`runCoreDiff`(`cmd/opencodereview/core_cmd.go:152`)가 diff 모드 검증을 **사설 복제본**으로
들고 있었다는 사실이다. 공유 함수 `validateDiffMode`는
`cmd/opencodereview/shared_flags.go:76-94`에 이미 있었고, `validateReviewOptions`(`:106`)와
`validateDelegateOptions`(`:158`)가 이를 쓰고 있었다.

> 정정: 이 세션의 초기 요약은 `review`/`scan`/`delegate` 셋이 공유한다고 봤지만,
> `validateScanOptions`(`cmd/opencodereview/shared_flags.go:135-155`)에는 diff 모드 검증이
> 없다. `ocr scan`은 전체 파일 스캔이라 `--from/--to/--commit` 축이 없기 때문이다.
> 현재 트리에서 `validateDiffMode` 호출부는 `review`, `delegate`, 그리고 수정 후의
> `core diff` 세 곳이다.

넓은 엔진의 **리뷰어 패스가 이걸 전부 놓쳤다.** 그중 하나는 담당 렌즈 자체가
코드 재사용이었다. 리뷰어 8명은 `docs/residual-review-findings/feat-ocr-core-local-merge-main.md:5`에
기록돼 있어 확인 가능하고, 그에 앞선 `ce-simplify-code` 3인 패스는 이 세션의 실행 기록일 뿐
저장소에 산출물이 남지 않아 트리로는 검증할 수 없다(합계 11은 그래서 세션 관찰치다). 게다가 복제본의 메시지는 이미 드리프트해 있었다 —
공유본은 `"only one review mode allowed (--from/--to or --commit)"`
(`cmd/opencodereview/shared_flags.go:85`), 제거된 사설 복제본은
`"only one diff mode allowed (--from/--to or --commit)"`였다(로컬 커밋 `9430e45`의 diff에서 확인).

교훈: 다중 페르소나 리뷰는 **한 파일 안에서 읽히는 결함**에 강하고, **저장소 전체에 걸친
중복**에는 의외로 약하다. 좁고 파일 단위인 엔진은 그 반대다.

### 6. 한 엔진의 지적을 만족시킨 수정이 새 결함을 만들 수 있다 — 두 번째 패스는 수정 후에도 돌려라

앞선 리뷰의 app-config 패리티 지적을 고치는 과정에서 `runCoreDiff`가
`resolveMaxTokens(tplDefault, appCfg, 0)`처럼 CLI 오버라이드 인자를 `0`으로 하드코딩했다.
`resolveMaxTokens`의 음수 검사(`cmd/opencodereview/shared.go:47-50`)는

```go
func resolveMaxTokens(templateDefault int, cfg *Config, cliOverride int) (int, error) {
	if cliOverride < 0 {
		return 0, fmt.Errorf("--max-tokens must be a non-negative integer")
	}
```

이므로, `0`을 넘기면 이 분기는 **영원히 도달 불가**가 된다. 결과적으로
`ocr core diff --max-tokens -1`은 조용히 exit 0으로 통과하고, 같은 플래그를
`ocr review`는 거부하는 비대칭이 생겼다(`ocr review` 쪽 검증은
`cmd/opencodereview/shared_flags.go:126-128`).

현재 트리에서는 고쳐져 있다 — `cmd/opencodereview/core_cmd.go:190`은
`resolveMaxTokens(tplDefault, appCfg, opts.maxTokens)`이고,
`cmd/opencodereview/core_cmd.go:153`은 공유 `validateDiffMode`에 위임한다.
둘 다 로컬(미푸시) 커밋 `9430e45`에서 적용됐다.

## Why This Matters

**"리뷰가 지적하지 않았다"는 두 가지 전혀 다른 뜻을 가진다.** 하나는 "봤고 문제없다고
판단했다", 다른 하나는 "필터가 잘라내서 애초에 보지 않았다". 후자를 전자로 착각하면
리뷰 커버리지를 실제보다 훨씬 넓게 신뢰하게 된다.

이 브랜치가 구체적인 예다. 세션 중 확인된 실제 결함 — `**/generated/*`를 권장 예시로 쓰던
문서 — 는 `docs/ocr-core-local-usage.ko-KR.md`와 `skills/open-code-review-local/SKILL.md`에
있었고, 두 파일 모두 `unsupported_ext`로 제외되는 마크다운이다. 같은 잘못된 패턴이
`cmd/opencodereview/core_cmd.go`의 `Example` 문자열에도 있었기 때문에 **운 좋게** Go 파일
경로로 발견됐을 뿐이다. 로컬(미푸시) 커밋 `b62c278`이 세 곳을 함께 `**/generated/**`로 고쳤다.

여기에 세 가지 실질적 비용이 걸려 있다.

1. **데이터 유출 축**: `ocr core diff`는 리뷰 대상 파일의 `new_file_content` 전문을 JSON으로
   내보낸다(`internal/diff/core_diff.go:52`). 사용자가 신뢰하는 exclude 패턴이 under-match
   하면 제외됐다고 믿은 파일 내용이 모델 컨텍스트로 그대로 나간다. 문서가 잘못된 패턴을
   권장하고 있고, 그 문서가 리뷰 대상에서 구조적으로 빠져 있다면 이 루프는 스스로 닫히지
   않는다.
2. **품질 축**: 테스트 파일이 전부 `default_path`로 빠지므로, 잘못된 assertion·죽은 테스트·
   커버리지 착시는 이 엔진으로는 절대 발견되지 않는다.
3. **중복 축**: 파일 단위 좁은 렌즈는 저장소 전역 중복을 잡고, 넓은 다중 페르소나 렌즈는
   놓친다는 사실이 이 세션에서 관찰됐다 — 넓은 쪽 리뷰어 패스 전체가 놓친 중복을 좁은 쪽은
   첫 패스에서 잡았다. 두 렌즈는 대체재가 아니라 보완재다.

### 정직한 한계 — 이 문서가 주장하지 **않는** 것

두 엔진은 **동일한 트리를 리뷰하지 않았다.** `ce-code-review`는 base `277bdd6`에 대해 더 이른
HEAD를 봤고(`docs/residual-review-findings/feat-ocr-core-local-merge-main.md:4`의 범위
`277bdd6..b62c278`), `open-code-review-local`은 그 수정들이 이미 반영된 뒤에 돌았다.
따라서 **양쪽의 지적 건수를 정밀도 비교로 쓸 수 없다.**

유효한 사각지대 증거는 딱 두 가지다.

- `validateDiffMode` 중복 — **두 엔진 모두 볼 수 있었던 코드**에서 한쪽만 잡았다.
- 구조적으로 제외된 9개 파일 — 필터 정의상 한쪽이 볼 수 **없었던** 코드다.

## When to Apply

- 리뷰 엔진·에이전트·스킬이 **프로덕션 필터를 재사용해** 대상 파일을 고를 때
  (이 저장소에서는 `ocr core diff`, `ocr review`, `ocr scan`이 같은 판정 알고리즘의 사본을
  쓴다 — `internal/diff/core_diff.go:175`, `internal/agent/preview.go:34`,
  `internal/scan/agent.go:461`).
- 리뷰 결과의 **침묵을 근거로** "이 영역은 문제없다"고 결론 내리려 할 때.
- 변경 묶음에 **테스트나 문서 변경이 섞여 있을 때** — 이 두 부류는 기본 필터에서 통째로
  빠진다.
- `--exclude` / `rule.json` exclude 패턴을 **처음 도입하거나 문서화할 때**. 특히 그 패턴이
  민감 디렉터리를 가린다고 믿고 있을 때.
- 이전 리뷰 지적을 반영한 **수정 커밋 이후**. 수정 자체가 새 결함을 만들 수 있으므로
  두 번째 패스는 수정 전이 아니라 수정 **후**에 값이 있다.
- 적용하지 **않아도** 되는 경우: 대상 파일 집합을 사람이 직접 지정한 리뷰
  (`ocr scan --path a.go,b.go` 같은 명시 목록)라면 필터 사각지대가 아니라 사람의 선택이므로,
  열거의 대상은 필터가 아니라 그 목록이다.

## Examples

### 예시 1 — 이 브랜치의 실제 파일 선정 (현재 트리에서 재현 가능)

```bash
ocr core diff --repo . --from 277bdd6 --to HEAD
```

결과 요약(설치된 `ocr` 바이너리로 실행 확인):

```
total 14   reviewable 5   excluded 9

cmd/opencodereview/core_cmd.go                                   ✓
cmd/opencodereview/core_cmd_test.go                              ✗ default_path
cmd/opencodereview/core_diff_wiring_test.go                      ✗ default_path
cmd/opencodereview/root.go                                       ✓
cmd/opencodereview/shared.go                                     ✓
docs/ocr-core-local-usage.ko-KR.md                               ✗ unsupported_ext
docs/residual-review-findings/feat-ocr-core-local-merge-main.md  ✗ unsupported_ext
internal/agent/agent.go                                          ✓
internal/agent/whyexcluded_parity_test.go                        ✗ default_path
internal/diff/core_diff.go                                       ✓
internal/diff/core_diff_test.go                                  ✗ default_path
internal/diff/core_filter_parity_test.go                         ✗ default_path
internal/model/diff_test.go                                      ✗ default_path
skills/open-code-review-local/SKILL.md                           ✗ unsupported_ext
```

14개 중 5개만 리뷰 대상이다. 제외 9개는 **테스트 6 + 마크다운 3**이고, 그중
`docs/ocr-core-local-usage.ko-KR.md`와 `skills/open-code-review-local/SKILL.md`가 바로
잘못된 glob 예시를 담고 있던 파일이다.

> 주의: 이 표는 2026-08-14 시점 스냅샷이다. `default_exclude_patterns.json`이나
> `supported_file_types.json`이 바뀌면 결과도 바뀐다. 이 문서를 믿지 말고 위 명령을
> 직접 돌려라.

### 예시 2 — glob under-match (before / after)

아래 경로는 저장소 파일이 아니라 **glob 동작을 보여주기 위한 가상 경로**다.

```bash
# BEFORE — 문서가 권장하던 패턴
ocr core diff --exclude '**/generated/*'
#   src/generated/api.go          → user_exclude   (걸러짐)
#   src/generated/deep/nested.go  → will_review    ← 사용자는 제외됐다고 믿는다.
#                                                    new_file_content 전문이 방출된다.

# AFTER — 하위 트리 전체를 덮는 패턴
ocr core diff --exclude '**/generated/**'
#   src/generated/api.go          → user_exclude
#   src/generated/deep/nested.go  → user_exclude
```

근거: 문법 문서 `internal/config/allowlist/allowed_ext.go:12-27`, 단위 테스트
`internal/diff/core_filter_parity_test.go:107-118`, CLI 통합 테스트
`cmd/opencodereview/core_diff_wiring_test.go:108-141`.

**아직 남아 있는 동일 결함(이 브랜치 변경 범위 밖):** 같은 under-match 예시가 여전히
`cmd/opencodereview/review_cmd.go:88`, `cmd/opencodereview/scan_cmd.go:71`,
그리고 `pages/src/content/docs/{en,zh,ja,ru}/cli-reference.md`의 `--exclude` 설명에 남아 있다.
관련해서 `addExcludeFlag`의 도움말도 실제 동작과 어긋난다 —
`cmd/opencodereview/shared_flags.go:40`은 `"comma-separated gitignore-style patterns to
exclude"`라고 하지만 실제 매칭은 전체 경로 대상 doublestar glob이다. 이 항목은
`docs/residual-review-findings/feat-ocr-core-local-merge-main.md`의 **R2**로 의도적으로 이월됐다.

### 예시 3 — 좁은 엔진이 잡은 중복 (before / after)

```go
// BEFORE — cmd/opencodereview/core_cmd.go, 사설 복제본
func runCoreDiff(opts coreDiffOptions) error {
	if (opts.from != "" || opts.to != "") && opts.commit != "" {
		return fmt.Errorf("only one diff mode allowed (--from/--to or --commit)")
	}
	if opts.from != "" && opts.to == "" {
		return fmt.Errorf("--to is required when --from is specified")
	}
	if opts.to != "" && opts.from == "" {
		return fmt.Errorf("--from is required when --to is specified")
	}
```

```go
// AFTER — cmd/opencodereview/core_cmd.go:152-155
func runCoreDiff(opts coreDiffOptions) error {
	if err := validateDiffMode(opts.from, opts.to, opts.commit); err != nil {
		return err
	}
```

공유본은 `cmd/opencodereview/shared_flags.go:76-94`. 메시지 드리프트
(`"diff mode"` vs `"review mode"`)가 복제본이 오래됐다는 신호였다.
넓은 엔진의 리뷰어 패스는 이걸 놓쳤고, 파일 단위 좁은 엔진은 첫 패스에서 잡았다.

### 예시 4 — 수정이 만든 새 결함 (before / after)

```go
// BEFORE — cmd/opencodereview/core_cmd.go, CLI 오버라이드를 0으로 하드코딩
maxTokens, err = resolveMaxTokens(tplDefault, appCfg, 0)
// → resolveMaxTokens의 `if cliOverride < 0` 분기(shared.go:48-50)가 도달 불가.
//   `ocr core diff --max-tokens -1` 이 exit 0. `ocr review --max-tokens -1` 은 거부.
```

```go
// AFTER — cmd/opencodereview/core_cmd.go:190
maxTokens, err = resolveMaxTokens(tplDefault, appCfg, opts.maxTokens)
// → 0일 때 동작은 동일하고, 음수 거부가 되살아나 `ocr review` 와 패리티가 맞는다.
```

이 결함은 **앞선 리뷰의 지적을 고치는 과정에서 새로 생겼고**, 두 번째 엔진의 패스에서만
드러났다. 로컬(미푸시) 커밋 `9430e45`에서 수정됐다.

### 예시 5 — 다음 사람이 직접 확인하는 절차

이 문서의 스냅샷을 믿지 말고, 아래 순서로 **현재 트리의** 동작을 확인하라.

1. 사유 상수 확인 — `internal/model/preview.go:11-17`
2. 판정 순서 확인 — `internal/diff/core_diff.go:163-200` (`coreWhyExcluded` → `coreFilterReason`)
3. 기본 제외 목록 확인 — `internal/config/allowlist/default_exclude_patterns.json`
4. 지원 확장자 확인 — `internal/config/allowlist/supported_file_types.json`
   (`.md`가 들어왔는지 여부가 문서 리뷰 가능성을 결정한다)
5. 실제 변경 묶음에 대해 `ocr core diff`를 돌려 `exclude_reason`별로 집계
6. 중첩 경로 프로브 — 임시 리포를 새로 만들고 그 안에 `a/b/c.go`(이 저장소의 파일이 아니라
   프로브용으로 직접 생성하는 파일)를 둔 뒤, `--exclude '**/b/*'` 와
   `--exclude '**/b/**'` 의 `exclude_reason` 차이를 직접 관찰

그리고 리뷰 리포트를 읽을 때는 항상 이 한 문장을 덧붙여라:
**"이 리뷰는 N개 파일 중 M개만 보았다. 나머지 N−M개는 다음 사유로 제외됐다."**

## Related

- `docs/residual-review-findings/feat-ocr-core-local-merge-main.md` — R1이 같은 필터 판정
  알고리즘의 3중 복제를, R2가 `--exclude` 도움말 문구 오류를 이월 항목으로 기록한다.
  이 학습은 그 지적들을 두 엔진 교차 실행으로 실측 확인한 증거다.
- `docs/ocr-core-local-usage.ko-KR.md` — `exclude_reason` 트러블슈팅 표와 패리티 미검증(U6)
  한계 섹션. 이 학습이 다루는 제외 사유가 사용자 시점에서 어떻게 보이는지.
- `skills/open-code-review-local/SKILL.md` — `ocr core diff`를 파일 선정에 쓰는 스킬 본체.
  이 학습의 사각지대는 이 스킬을 쓰는 모든 리뷰에 적용된다.
- `docs/plans/2026-08-14-001-merge-origin-main-ocr-core-plan.md` — 현재 필터 구현이 유래한
  병합 계획.

업스트림 이슈 (`alibaba/open-code-review`) — 같은 패턴의 선례:

- [#782](https://github.com/alibaba/open-code-review/issues/782) (OPEN) — `--preview`가
  "Will review"로 보고한 파일을 실제 실행은 dispatch 전에 버린다. 커버리지가 추론이 아니라
  필터/확정 시점의 속성이라는 이 학습의 주장과 정확히 같은 형태의 별개 사례.
- [#844](https://github.com/alibaba/open-code-review/issues/844) (CLOSED) — FileFilter가
  경로만 소문자화하고 패턴은 안 해서 대문자 포함 패턴이 전혀 매칭되지 않던 결함.
- [#650](https://github.com/alibaba/open-code-review/issues/650) (CLOSED) — `.gitignore`
  부정 패턴이 버려져 allow-list 저장소가 조용히 0개 파일을 리뷰하던 결함.
- [#369](https://github.com/alibaba/open-code-review/issues/369) (OPEN) — 여러 리뷰 실행
  결과를 지문/의미론적 군집으로 매칭하는 문제. 교차 실행 결과 병합이라는 이 학습의
  방법론과 인접.
