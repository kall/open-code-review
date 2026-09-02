---
title: 텍스트 충돌 없는 병합은 브랜치 전용 파일이 컴파일된다는 증거가 아니다
date: 2026-09-02
category: workflow-issues
module: upstream-sync
problem_type: workflow_issue
component: development_workflow
severity: high
related_components:
  - tooling
  - testing_framework
  - documentation
applies_when:
  - "장기 기능 브랜치가 upstream 태그나 origin/main을 주기적으로 병합할 때"
  - "브랜치 전용 파일이 upstream 패키지의 함수 시그니처를 직접 호출할 때"
  - "`git merge-tree --write-tree`의 충돌 수만 보고 병합 난이도를 판단하려 할 때"
  - "충돌 파일을 `git checkout --theirs`나 `--ours`로 일괄 해소하려 할 때"
  - "브랜치가 upstream 코드 일부를 삭제·이동한 상태에서 upstream 변경을 받아들일 때"
symptoms:
  - "`git merge-tree`가 충돌 2건만 보고했으나 브랜치 전용 `core_cmd.go`는 손대지 않은 채 빌드 실패"
  - "`go build ./...`가 not enough arguments in call to rules.NewResolver 로 실패"
  - "upstream PR #574가 `rules.NewResolver` 시그니처에 `ResolverOptions{Ref, Runner}`를 추가했으나 충돌로 드러나지 않음"
  - "`--theirs`로 해소한 `agent_test.go`가 브랜치에서 삭제한 TestExtFromPath를 되살려 go vet 실패"
  - "2026-08-14 origin/main 병합에서도 같은 종류의 무충돌 빌드 파손(flags.go 삭제)이 발생한 두 번째 사례"
root_cause: missing_workflow_step
resolution_type: workflow_improvement
tags:
  - upstream-sync
  - trial-merge
  - git-worktree
  - merge-tree
  - branch-only-code
  - api-signature-drift
  - conflict-resolution
  - ocr-core
---

# 텍스트 충돌 없는 병합은 브랜치 전용 파일이 컴파일된다는 증거가 아니다

## Context

`feat/ocr-core-local`은 upstream(alibaba/open-code-review)에 없는 브랜치 전용 명령 그룹 `ocr core`(Core command group)를 얹은 장수(long-lived) 브랜치다. 브랜치 전용 파일은 `cmd/opencodereview/core_cmd.go`, `internal/diff/core_diff.go`, `skills/open-code-review-local/SKILL.md` 등이며, upstream 릴리스 태그를 주기적으로 병합해 따라간다.

2026-09-02 세션에서는 `v1.9.3` 기반 브랜치에 `v1.11.2`(99 커밋 차이)를 병합했다. `git merge-tree`가 보고한 충돌은 `internal/agent/agent.go`, `internal/agent/agent_test.go` 두 파일뿐이었고 내용도 사소했다. 브랜치가 `(*Agent).extFromPath`를 `model.ExtFromPath`(`internal/model/diff.go:58`)로 추출하면서 원본 메서드와 `TestExtFromPath`를 삭제했는데, upstream이 그 인접 줄을 편집하고 바로 그 자리에 새 테스트를 추가한 것이 전부였다.

그러나 스크래치 워크트리에서 병합 후 `go build ./...`를 돌리자 **충돌이 0건이던 파일**에서 컴파일이 깨졌다.

```
cmd/opencodereview/core_cmd.go:197:56: not enough arguments in call to rules.NewResolver
	have (string, string)
	want (string, string, rules.ResolverOptions)
cmd/opencodereview/core_cmd.go:366:54: (same)
```

원인은 upstream이 `.m` 확장자(MATLAB vs Objective-C)의 규칙 문서를 내용 스니핑으로 고르도록 하면서 `rules.NewResolver`의 시그니처를 `(repoDir, customRulePath string, opts ResolverOptions)`로 바꾼 것이다(`internal/config/rules/system_rules.go:300`). `git log -S 'ResolverOptions'` 기준 이 변경을 도입한 것은 #574(feat(allowlist): add matlab support)다. 같은 주기에 같은 파일을 손댄 upstream 보안 수정 커밋 `124bfc3`(제목 "fix(security): prevent rule.json from reading arbitrary files on the review host (#1100)", rule.json이 저장소 밖 파일을 읽지 못하게 경로 제한)은 시그니처를 건드리지 않았는데, 이 세션은 처음에 그 커밋을 원인으로 잘못 짚었고 병합 커밋 본문에도 "#1100"으로 남아 있다. 덧붙여 GitHub PR #1100 자체는 그 보안 수정의 Windows 테스트 후속 수정이라, 커밋 제목의 "(#1100)" 라벨과 PR 내용이 일치하지 않는다. `core_cmd.go`는 upstream에 대응 파일이 없으므로 git이 충돌시킬 대상이 없었고, 깨짐은 컴파일 시점에야 드러났다.

이 패턴은 이 브랜치에서 **두 번째**다. 2026-08-14 origin/main 병합 계획(`docs/plans/2026-08-14-001-merge-origin-main-ocr-core-plan.md` 2.2절)은 upstream이 Cobra 이관(#625) 중 `cmd/opencodereview/flags.go`를 삭제해(현재 트리에는 없는 파일) `core_cmd.go`가 `undefined: newOcrFlagSet` 5건으로 깨질 것을 기록했다. 브랜치 전용 파일, 충돌 0건, 컴파일 실패라는 형태가 동일하다.

주목할 점은 당시 계획이 이미 "자동 병합되지만 컴파일이 깨지는 지점 (충돌보다 위험)"이라는 절을 두고 이 위험을 인식하고 있었다는 것이다 (session history). 그러나 그 검출은 `git merge-tree` 시뮬레이션에 더해 삭제된 함수명과 옛 import 경로를 **grep과 육안 대조**로 찾는 방식이었고, 컴파일 복구는 병합 **이후** 단계로 배치됐다 (session history). grep은 사라진 심볼은 잡지만 `NewResolver`처럼 이름은 그대로고 인자만 늘어난 시그니처 변경은 잡지 못한다. 패턴 인식만으로는 부족했고, 컴파일러를 병합 **앞**에 세우는 절차가 없었던 것이 두 번째 사례를 만들었다.

## Guidance

upstream 태그를 병합할 때 아래 순서를 고정 절차로 삼는다. 핵심은 **실제 브랜치를 건드리기 전에 스크래치 워크트리에서 병합·빌드·vet·테스트를 끝까지 돌려보는 것**이다.

### 1. 트리를 건드리지 않고 드라이런

```bash
# 충돌 파일 목록 + 병합 결과 트리 OID (워크트리 무변경)
git merge-tree --write-tree --name-only HEAD v1.11.2

# 충돌 훅 내용 확인 (첫 줄의 OID 사용)
git show <tree-oid>:internal/agent/agent_test.go | grep -n '^<<<<<<<\|^=======\|^>>>>>>>'

# 양쪽이 모두 건드린 파일 (충돌 여부와 무관)
comm -12 \
  <(git diff --name-only v1.9.3 HEAD | sort) \
  <(git diff --name-only v1.9.3 v1.11.2 | sort)
```

충돌 수가 적어도 안심하지 않는다. 이 단계의 목적은 "무엇을 손으로 풀어야 하는가"를 아는 것이지 "병합이 안전한가"를 판단하는 것이 아니다.

### 2. 스크래치 워크트리에서 시험 병합 후 빌드·vet·테스트

```bash
SCRATCH=<세션 스크래치 디렉터리>/scratch-merge
git worktree add --detach "$SCRATCH" HEAD
cd "$SCRATCH"
git merge --no-commit --no-ff v1.11.2
# ... 충돌 해소 ...
go build ./... && go vet ./... && go test ./...
```

빌드 오류가 **충돌 목록에 없던 파일**에서 나오면 그것이 바로 브랜치 전용 파일의 API 드리프트다. 프로젝트 표준 `make check`는 라이선스·영어 전용 검사, `go mod tidy`, gofmt, vet을 돌리지만 테스트는 포함하지 않으므로(`Makefile:73-76`), `go test ./...`를 별도로 붙인다.

이 세션에서는 `/tmp`가 tmpfs인 머신이라 `internal/tool`의 `TestGitGrep_NonGitDirectory*`가 git의 파일시스템 경계 정지에 걸려 실패했고, `GIT_DISCOVERY_ACROSS_FILESYSTEM=1`을 붙여야 통과했다. HEAD와 upstream 양쪽에서 동일하게 재현되는 병합 무관 현상이므로, 시험 병합 결과를 읽을 때 기준선(병합 전 HEAD)에서 같은 테스트를 한 번 돌려 환경 실패와 병합 실패를 분리한다.

### 3. `--theirs`/`--ours` 후에는 브랜치가 지운 심볼이 되살아났는지 확인

```bash
git checkout --theirs -- internal/agent/agent_test.go
# 브랜치가 base 대비 삭제한 함수/테스트가 다시 들어왔는지 본다
git diff v1.9.3 HEAD -- internal/agent/agent_test.go | grep '^-func '
grep -n 'func TestExtFromPath\|extFromPath' internal/agent/agent_test.go
```

한쪽을 통째로 택하면 그쪽이 아직 갖고 있는 심볼이 그대로 따라온다. 브랜치가 삭제한 정의를 upstream이 유지하고 있었다면 참조는 살아나고 정의는 없으므로 `go vet`이 실패한다. 통째로 택한 뒤 브랜치 쪽 삭제를 다시 적용한다.

### 4. 브랜치 전용 호출부는 가장 가까운 upstream 호출부를 그대로 따른다

새 옵션 값을 스스로 고안하지 말고, 같은 의미의 upstream 명령이 무엇을 넘기는지 찾아 그대로 복제한다.

- ref를 아는 명령(`--from/--to/--commit`이 있는 `core diff`)은 `ocr review`(`cmd/opencodereview/review_cmd.go:123`)처럼 `tool.ParseReviewMode(...).RefValue(...)`로 얻은 ref를 `ResolverOptions{Ref: contentRef}`에 넣는다.
- 워킹트리 기준 명령(`core rule`)은 `ocr rules check`(`cmd/opencodereview/rules_cmd.go:51`)처럼 `rules.ResolverOptions{}` 제로값을 쓴다.

`ResolverOptions.Ref`의 의미는 소스 주석(`internal/config/rules/system_rules.go:279-281`)에 명시되어 있다. range 모드에서는 리뷰 head(`--to`), commit 모드에서는 `--commit`, 비어 있으면 워킹트리를 읽으며 이것이 `ocr scan`과 `ocr rules check`가 원하는 동작이다. `Runner`는 git 서브프로세스 동시 실행 수를 제한하는 선택 필드로, nil이면 git을 직접 실행한다(`system_rules.go:284-286`). `ocr review`의 공용 경로(`cmd/opencodereview/shared.go:112-115`)는 `Ref`와 `Runner`를 모두 채우지만, 파일 하나당 최대 한 번 읽는 `.m` 스니핑만을 위해 `core diff`에는 `Ref`만 넘겼다. 미러링할 때 이렇게 의도적으로 뺀 필드는 그 이유를 커밋 본문이나 주석에 적어 두는 것이 다음 병합 때의 재판단 비용을 줄인다.

### 5. 병합 커밋 본문에 "침묵 파손"과 그 수정을 기록한다

충돌 해소 내용과 함께, 충돌 없이 깨진 파일과 어떤 upstream 호출부를 따라 고쳤는지 적는다. 태그 `v1.11.2-local`이 가리키는 병합 커밋(Merge tag 'v1.11.2' into feat/ocr-core-local) 본문이 그 예다. 다음 병합 때 같은 파일을 먼저 의심할 수 있는 단서가 된다. 단, 원인 PR 번호는 `git log -S <새 심볼>`로 확정한 뒤 적는다. 이 세션은 그 확인을 문서화 단계에서야 해서 커밋 본문에 잘못된 PR 번호가 남았다.

## Why This Matters

git의 충돌 감지는 **텍스트 기반이고 파일 단위**다. 세 방향 병합은 같은 파일의 같은 영역을 양쪽이 바꿨을 때만 충돌을 낸다. 브랜치 전용 파일은 upstream 쪽 대응물이 없으므로, 그 파일이 호출하는 upstream API가 아무리 바뀌어도 병합 알고리즘 입장에서는 "한쪽만 바뀐 파일"이라 그냥 통과한다. 따라서 API 드리프트는 **구조적으로 충돌 목록에 나타날 수 없다**.

결과적으로 충돌 수는 병합 안전성의 대리 지표가 아니다. 이 브랜치에서는 충돌 0건 파일이 컴파일을 깨뜨리는 일이 2026-08-14(#625, `flags.go` 삭제)와 2026-09-02(#574, `NewResolver` 시그니처 변경) 두 번 반복됐다. 두 번 모두 컴파일러가 잡았지만, 스크래치 워크트리 없이 실제 브랜치에서 바로 `git merge`했다면 깨진 상태의 병합 커밋을 만들거나 되돌리는 비용이 들었을 것이다. 그리고 grep 기반 사전 점검은 첫 사례 유형(삭제된 심볼)만 잡을 수 있었고, 두 번째 유형(인자가 늘어난 시그니처)은 컴파일러만 잡을 수 있었다.

`--theirs` 함정도 같은 뿌리다. 파일 단위로 한쪽을 택하는 순간 그 파일 안의 브랜치 측 삭제는 모두 사라진다. 삭제는 "없는 것"이라 diff에서 눈에 잘 띄지 않으므로, 되살아난 심볼은 vet가 아니면 놓치기 쉽다.

이 교훈은 같은 브랜치의 선행 학습(리뷰 엔진의 침묵을 무결함의 증거로 읽지 말라)과 같은 인식론적 오류의 다른 사례다. 어떤 도구가 "아무 말도 하지 않았다"는 사실은 그 도구가 볼 수 있는 범위 안에서만 의미가 있고, 병합 알고리즘이 볼 수 있는 범위에 브랜치 전용 파일의 API 의존성은 들어 있지 않다.

## When to Apply

- upstream에 없는 파일(명령, 패키지, 스킬)을 가진 장수 브랜치가 upstream 릴리스 태그나 main을 병합할 때.
- `git merge-tree`가 보고한 충돌이 0건이거나 사소해 보일 때. 오히려 이때가 빌드 검증을 건너뛰기 쉬운 순간이다.
- 브랜치 전용 파일이 upstream 내부 패키지(`internal/config/rules`, `internal/tool` 등)의 함수를 직접 호출하고 있을 때.
- 충돌 해소에서 파일 단위 `--theirs`/`--ours`를 쓸 때. 그 파일에 브랜치 측 삭제가 있었다면 반드시 재적용을 확인한다.
- Go처럼 컴파일러가 시그니처 불일치를 잡아주는 언어라도 적용한다. 컴파일 단계를 실제 병합 이후로 미루는 것이 문제이며, 동적 언어라면 테스트가 그 역할을 대신해야 하므로 더욱 필요하다.

## Examples

### `NewResolver` 시그니처 변경에 대한 브랜치 전용 호출부 수정

병합 전(브랜치, `v1.9.3` 시절 API):

```go
_, fileFilter, err := rules.NewResolver(resolvedRepo, opts.rulePath)
// ...
resolver, _, err := rules.NewResolver(resolvedRepo, rulePath)
```

병합 후(`v1.11.2-local` 태그 시점, `core diff`는 `review_cmd.go:123`을, `core rule`은 `rules_cmd.go:51`을 그대로 따름):

```go
// cmd/opencodereview/core_cmd.go:198-199  (core diff: ref-aware)
contentRef, _ := tool.ParseReviewMode(opts.from, opts.to, opts.commit).RefValue(opts.to, opts.commit)
_, fileFilter, err := rules.NewResolver(resolvedRepo, opts.rulePath, rules.ResolverOptions{Ref: contentRef})

// cmd/opencodereview/core_cmd.go:368  (core rule: working tree)
resolver, _, err := rules.NewResolver(resolvedRepo, rulePath, rules.ResolverOptions{})
```

바뀐 시그니처와 옵션 정의는 `internal/config/rules/system_rules.go:278-300`에 있다.

### `--theirs`가 되살린 삭제 심볼

```bash
git checkout --theirs -- internal/agent/agent_test.go
go vet ./...
# internal/agent/agent_test.go:345: a.extFromPath undefined
#   (type *Agent has no field or method extFromPath)
```

upstream의 새 테스트 `TestParseFilterToolCalls`(현재 `internal/agent/agent_test.go:235`)를 얻는 대신, 브랜치가 지운 `TestExtFromPath`가 함께 돌아왔다. `agent.go`에는 이미 `extFromPath`가 없으므로(`model.ExtFromPath`, `internal/model/diff.go:58`로 이동) 참조만 남아 vet가 실패했다. 해결은 theirs를 택한 뒤 `TestExtFromPath`를 다시 제거하는 것이다.

### 시험 병합 전체 시퀀스

```bash
# 1. 드라이런
git merge-tree --write-tree --name-only HEAD v1.11.2

# 2. 스크래치 워크트리에서 시험 병합
git worktree add --detach "$SCRATCH" HEAD
cd "$SCRATCH"
git merge --no-commit --no-ff v1.11.2
git checkout --theirs -- internal/agent/agent_test.go   # 이후 브랜치 측 삭제 재적용
# agent.go: 브랜치 측 extFromPath 삭제 유지
# core_cmd.go: NewResolver 호출부 2곳을 review_cmd.go / rules_cmd.go 방식으로 수정
go build ./... && go vet ./... && go test ./...

# 3. 통과하면 실제 브랜치에서 같은 절차를 반복해 커밋
cd <repo-root>
git merge --no-commit --no-ff v1.11.2
# 동일 해소 적용 → 빌드/vet/테스트 → 커밋 본문에 침묵 파손 기록
git worktree remove "$SCRATCH"
```

이 절차를 따랐기 때문에 실제 브랜치에는 깨진 상태가 한 번도 올라가지 않았고, 병합 커밋 하나에 충돌 해소와 API 적응이 함께 담겨 그 커밋을 `v1.11.2-local`로 태그·릴리스할 수 있었다.

## Related

- `docs/plans/2026-08-14-001-merge-origin-main-ocr-core-plan.md` 2.2절 — 첫 번째 무충돌 빌드 파손(`flags.go` 삭제, #625) 기록. "자동 병합되지만 컴파일이 깨지는 지점"을 이미 별도 절로 다뤘으나 검출은 grep·육안 대조였다.
- `docs/residual-review-findings/feat-ocr-core-local-merge-main.md` — 브랜치 전용 코드가 upstream 공유 코드에 의존하는 지점(필터 3중 복제, `--rule` 대체 시맨틱) 목록. 다음 병합에서 먼저 의심할 파일 후보.
- `docs/solutions/workflow-issues/cross-engine-review-exposes-file-filter-blind-spots.md` — 같은 브랜치의 선행 학습. "리뷰 침묵 ≠ 무결함"과 "무충돌 병합 ≠ 컴파일 성공"은 같은 오류의 두 사례.
- `CONCEPTS.md` — Core command group(브랜치 전용 계층), Review parity(사본 간 일치는 테스트로만 보증된다).
- upstream PR #574 (`ResolverOptions` 도입), #625 (Cobra 이관, `flags.go` 삭제), 커밋 `124bfc3` (rule.json 경로 제한 보안 수정, 제목에 "(#1100)" 표기, 시그니처 변경 아님).
