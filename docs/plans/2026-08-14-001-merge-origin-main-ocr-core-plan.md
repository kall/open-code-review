# feat/ocr-core-local ← origin/main 병합 검토 및 패치 계획

- 작성일: 2026-08-14
- 대상 브랜치: `feat/ocr-core-local`
- 병합 기준점: `41620dc` (docs(pages): document PowerShell install.ps1, #399)
- origin/main 선행 커밋 수: 약 200개
- 검증 방법: `git merge-tree --write-tree HEAD origin/main` 병합 시뮬레이션 + 병합 결과 트리 분석

---

## 1. 배경

`feat/ocr-core-local`은 API key 없이 동작하는 `ocr core` 명령 그룹(결정적 diff 수집·규칙·프롬프트 빌딩 블록)과
`open-code-review-local` 스킬을 추가한 브랜치다. 마지막 동기화(41620dc) 이후 origin/main에는
아래 세 건의 대형 구조 변경이 들어왔고, 이것이 병합 난이도의 핵심이다.

| 변경 | PR | 영향 |
|---|---|---|
| Go 모듈 경로 변경 `github.com/open-code-review/open-code-review` → `github.com/alibaba/open-code-review` | #526 | 브랜치 신규 파일의 import 전면 치환 필요 |
| CLI를 Cobra 프레임워크로 전면 이관 (main.go 112줄 수동 switch → 29줄 `rootCmd.Execute()`) | #625 | `ocr core` 명령 등록 방식 재작성 필요 |
| CI 게이트 추가: SPDX 헤더 검증, 영어 전용 소스 검사, 커버리지 90% 강제 | #740, #876, #747 | 신규 파일 헤더 추가·커버리지 보강 필요 |

---

## 2. 검토사항 (병합 시 발생 이슈)

### 2.1 텍스트 충돌 — 2개 파일

#### (A) `cmd/opencodereview/main.go` — 사실상 재작성

- 브랜치: 수동 `switch args[0]` 디스패치에 `case "core"` 추가 (112줄).
- origin/main: Cobra 기반으로 전면 교체 (29줄, `rootCmd.Execute()`), 서브커맨드는 파일별 `cobra.Command`로 분리.
- 충돌 해소가 아니라 **`core` 명령을 Cobra `cobra.Command`로 재작성**해야 한다.
- main에 추가된 CLI 컨벤션도 준수 필요:
  - 부모 커맨드의 unknown subcommand 거부 (#660, #694)
  - 예상치 못한 positional 인자 거부 및 기대 인자 안내 (#749, #892, `arg_errors.go`)
  - shell completion (#625)

#### (B) `internal/agent/agent.go:743` — 소규모 충돌, 양측 모두 반영

- 브랜치: `effectivePath(d)` → `d.EffectivePath()` 메서드 호출로 교체 (helper의 model 패키지 추출).
- origin/main: 같은 줄에 `a.markReused(d)` 호출 추가 (#582 per-file terminal state 관련).
- 해소: 두 변경을 모두 취한다.
  ```go
  a.session.RecordReviewItemReused(d.EffectivePath(), d.OldPath, d.NewPath, fingerprint, resume.SessionID, item.Comments)
  a.markReused(d)
  ```

### 2.2 자동 병합되지만 컴파일이 깨지는 지점 (충돌보다 위험)

#### (C) 구 모듈 경로 import 잔존 — 즉시 빌드 실패

병합 결과 `go.mod`는 `module github.com/alibaba/open-code-review`가 되지만,
브랜치 신규 파일 3개가 구 경로를 import한 채 충돌 없이 병합된다.

- `cmd/opencodereview/core_cmd.go`
- `internal/diff/core_diff.go`
- `internal/diff/core_diff_test.go`

#### (D) 삭제된 helper의 잔존 호출 — 빌드 실패

브랜치가 `effectivePath` / `diffStatus` / `extFromPath` 함수를 삭제하고 `model.Diff` 메서드로 이관했는데,
origin/main이 그 사이 추가한 새 호출부가 충돌 없이 자동 병합되어 정의 없는 함수 호출이 남는다.

- 확인된 지점: 병합 결과 `internal/agent/agent.go:985` — `Path: effectivePath(d),` → `d.EffectivePath()`로 교체 필요.
- 병합 후 `effectivePath(|diffStatus(|extFromPath(` 전체 grep으로 추가 잔존 호출 유무 재확인 필요.

치환 대응은 셋이 서로 다르다. `extFromPath`만 메서드가 아니라 패키지 함수로 이관됐다.

| 삭제된 helper | 치환 대상 | 잔존 호출부가 있는 패키지 |
|---|---|---|
| `effectivePath(d)` | `d.EffectivePath()` (메서드) | `internal/agent` |
| `diffStatus(d)` | `d.Status()` (메서드) | `internal/agent` |
| `extFromPath(p)` | `model.ExtFromPath(p)` (**패키지 함수**) | `internal/scan` |

grep 범위는 `internal/agent`(`preview.go`·`agent.go`·`preview_test.go`·`agent_test.go`)와
`internal/scan`(`agent.go`·`coverage_test.go`) 양쪽이다. 정의부가 origin/main에서
`internal/agent/preview.go`·`internal/scan/agent.go`로 옮겨졌으므로
`internal/agent/agent.go`만 확인하면 `internal/scan` 쪽 잔존 호출을 놓친다.

#### (D-2) 삭제된 `flags.go`의 잔존 호출 — 빌드 실패 (2.2에서 가장 큰 항목)

origin/main이 Cobra 이관(#625)에서 `cmd/opencodereview/flags.go`(343줄, `ocrFlagSet` 타입 전체)를 삭제했는데,
브랜치의 `runCoreDiff` / `runCoreRelocate` / `runCoreEmit` / `runCoreRule` / `runCorePrompt`가 모두
`newOcrFlagSet`으로 플래그를 파싱한다. (C)(D)를 모두 고쳐도 병합 결과 빌드는
`cmd/opencodereview/core_cmd.go:54,127,200,268,306: undefined: newOcrFlagSet` 5건으로 멈춘다.

따라서 Phase 2-6의 "기존 `runCoreXxx(args)` 함수를 최대한 보존" 전략은 성립하지 않는다.
`cmd/opencodereview/core_cmd_test.go`도 `runCore` / `runCorePrompt` / `runCoreDiff`를 args 슬라이스로
직접 호출하므로 함께 갱신해야 Phase 3-11의 커버리지 측정이 가능하다.

### 2.3 병합 후 CI 게이트 실패 예상 항목

#### (E) SPDX 라이선스 헤더 누락 — `verify-license.sh` 실패 확정

main의 CI는 모든 `.go/.sh/.js/.mjs/.ts/.tsx` 파일에 아래 헤더를 요구한다.

```go
// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors
```

헤더가 없는 브랜치 신규 파일:

- `cmd/opencodereview/core_cmd.go`
- `cmd/opencodereview/core_cmd_test.go`
- `internal/diff/core_diff.go`
- `internal/diff/core_diff_test.go`
- `internal/model/diff_test.go`

#### (F) 커버리지 90% 게이트 — 리스크

- main CI는 전체 statement coverage 90% 미만이면 실패 (#747).
- 브랜치는 구현 약 570줄 추가 대비 테스트가 상대적으로 얇고, 기존 테스트 147줄을 삭제(이관)했다.
- **기준선 실측치(2026-08-14, 병합 시뮬레이션 트리 기준): 전체 90.3%, `cmd/opencodereview` 패키지 85.4%.**
  게이트를 0.3%p 차로 통과하는 상태다. 즉 신규/변경 코드가 커버리지를 조금이라도 끌어내리면 바로 실패한다.
- 특히 Cobra 재작성에서 `runCore` 수동 디스패처와 `printCoreUsage`(현재 커버리지 0%)를 어떻게 처리하느냐가
  총계를 90% 아래로 뒤집을 수 있는 지점이다. 재작성 시 이 두 경로를 그대로 남기지 말고
  Cobra의 커맨드 트리·`RunE` 에러 경로 테스트로 대체해 커버리지를 확보한다.
- Phase 2 착수 전 Phase 0에서 기준선을 재측정하고, 재작성 후 총계가 90.3% 이상을 유지하는지 대조한다.

#### (G) 영어 전용 소스 검사 — 통과 예상 (확인 완료)

- `verify-english-only.go`는 `.go/.ts/.sh/.yml` 등 소스 확장자만 검사하고 `.md`는 제외.
- 한국어 문서(`docs/ocr-core-local-usage.ko-KR.md`, brainstorm/plan md)는 안전.
- 브랜치 신규 Go 파일 5개 전부 한글 0줄 확인.
- 주의: 추적 예정인 `build.sh` 등 스크립트에는 한글 주석 금지.

### 2.4 충돌 없이 병합되지만 기능 재검토가 필요한 겹침

#### (H) `internal/diff` 필터링 로직의 main 측 개선 사항

`ComputeCoreDiff`는 자체 diff 수집 로직을 갖고 있지 않다. `NewCommitProvider` / `NewWorkspaceProvider` /
`ParseHunks`와 `internal/config/allowlist`를 그대로 재사용하므로, 아래 main 개선 중 6건은 **자동 반영**된다.
따라서 이 6건은 별도 조치 대상이 아니고, 회귀 테스트만 확인하면 된다.

| main 개선 | 반영 경로 | 조치 |
|---|---|---|
| gitignore 부정(`!`) 패턴 지원 (#651) | 공용 Provider | 자동 반영 |
| gitignore 디렉터리 패턴 매칭 (#853) | 공용 Provider | 자동 반영 |
| snapshot/testdata/fixtures/generated 제외 (#683) | 공용 `allowedext.IsExcludedPath` | 자동 반영 |
| 머지 커밋 first-parent 기준 리뷰 (#450) | `internal/diff/git.go` | 자동 반영 |
| 바이너리 마커 앵커링·hunk 기준 +/- 카운트 (#451) | `internal/diff/parser.go` | 자동 반영 |
| diff 프롬프트에서 index 헤더 제거 (#609) | `internal/diff/parser.go` | 자동 반영 — 단, **출력 변화 확인 필요** |
| FileFilter 패턴 대소문자 무시 (#859) | `internal/config/rules` | **해당 없음 → 배선 필요** |

실제 갭은 마지막 한 건뿐이다. `CoreDiffOptions`에는 필터 필드가 없고 `coreWhyExcluded`는
"without the user-configured FileFilter, which the core command does not currently accept"라고
미지원을 명시하며, `runCoreDiff`도 `--exclude` 계열 플래그를 등록하지 않는다. 즉 #859는
테스트를 추가한다고 일치하게 되는 항목이 아니라 **`ocr core diff`에 FileFilter를 배선할지 여부의 설계 결정**이다.

**결정(2026-08-14): 배선한다.** `ocr core diff`와 `ocr review`의 파일 선정 결과는 일치해야 하므로,
"core는 사용자 필터를 받지 않는다"를 공개 계약으로 고정하지 않고 FileFilter를 core에 배선한 뒤 정합성을 검증한다.

부수 논점: #609의 index 헤더 제거는 `ocr core diff` JSON의 `diff` 필드 출력을 조용히 바꾼다.
이는 divergence가 아니라 자동 수렴이지만, core 출력 형식이 바뀌는 것이므로 골든 테스트로 확인하고
바뀌면 사용법 문서·스킬 문서의 출력 예시를 갱신한다.

#### (I) allowlist 언어 추가

Nim, Nix, Haskell, Julia, GraphQL, Bicep, HCL/Terraform, Protocol Buffers, Prisma 등 추가됨.
`core_diff.go`는 `internal/config/allowlist`를 재사용하므로 자동 반영 — 별도 조치 불필요(회귀 테스트만).

#### (J) 인접 영역 리팩터링

- `9ab0616` agent/scan 80% 토큰 임계 공유 — 브랜치가 만진 `internal/agent`, `internal/scan`과 동일 영역.
- 병합 후 두 패키지 테스트 일괄 실행으로 회귀 확인.

---

## 3. 패치 계획

### Phase 0 — 기준선 측정 (병합 전)

0. origin/main 체크아웃 상태에서 커버리지 기준선을 기록한다. 이 값이 Phase 3-11의 합격선이 된다.
   ```bash
   go test -race -count=1 -coverprofile=coverage.out ./...
   go tool cover -func=coverage.out | grep total:
   go tool cover -func=coverage.out | grep 'cmd/opencodereview'
   ```
   2026-08-14 실측치는 전체 90.3% / `cmd/opencodereview` 85.4%다(2.3(F) 참조). 값이 달라졌으면 2.3(F)를 갱신한다.

### Phase 1 — 병합 및 충돌 해소

1. `git fetch origin && git merge origin/main` 실행.
2. `internal/agent/agent.go` 충돌 해소: 2.1(B)대로 `d.EffectivePath()` + `a.markReused(d)` 모두 반영.
3. `cmd/opencodereview/main.go` 충돌 해소: origin/main 버전(Cobra 29줄)을 채택. `case "core"` 추가분은 버린다 (Phase 2에서 Cobra로 재등록).

### Phase 2 — 컴파일 복구

4. import 경로 일괄 치환:
   ```bash
   grep -rl 'github.com/open-code-review/open-code-review' --include='*.go' \
     | xargs sed -i 's|github.com/open-code-review/open-code-review|github.com/alibaba/open-code-review|g'
   ```
5. 잔존 helper 호출 정리: 2.2(D)의 치환 대응표대로 교체한다. `extFromPath`만 메서드가 아니라
   패키지 함수 `model.ExtFromPath(p)`이므로 주의. grep 범위는 `internal/agent`와 `internal/scan` 양쪽.
6. `core_cmd.go`를 Cobra로 재작성:
   - `coreCmd`(부모) + `diff/relocate/emit/rule/prompt` 서브커맨드를 `cobra.Command`로 정의, `rootCmd.AddCommand(coreCmd)`.
   - **플래그 파싱 이관 (2.2(D-2))**: `newOcrFlagSet` 기반 파싱을 각 `cobra.Command`의
     `Flags().StringVarP` / `BoolVarP` / `IntVar`로 옮기고 함수 시그니처를 `RunE(cmd, args)`로 바꾼다.
     origin/main이 `flags.go`를 삭제했으므로 기존 `runCoreXxx(args)` 시그니처는 보존할 수 없다.
   - `core_cmd_test.go`의 `runCore` / `runCorePrompt` / `runCoreDiff` 직접 호출 테스트를 새 시그니처에 맞게 갱신한다.
   - `arg_errors.go` 컨벤션에 맞춰 positional 인자 검증 적용 (#749, #892).
   - unknown subcommand 시 에러 반환 (#660) — Cobra 부모 커맨드 패턴(#694) 준수.
7. `go build ./... && go vet ./...` 통과 확인.

### Phase 3 — CI 게이트 대응

8. 신규 Go 파일 5개에 SPDX 헤더 추가 (2.3(E) 목록). `bash scripts/verify-license.sh`로 검증.
9. `go run scripts/verify-english-only.go` 실행 — 통과 예상이나 확인.
10. `gofmt -s -l .` 무출력 확인 (main CI는 gofmt -s 강제).
11. 커버리지 측정:
    ```bash
    go test -race -count=1 -coverprofile=coverage.out ./...
    go tool cover -func=coverage.out | grep total:
    ```
    90% 미달 시 `core_cmd.go`의 Cobra 래핑·에러 경로 중심으로 테스트 보강.

### Phase 4 — 기능 정합성 검증

12. FileFilter 배선 및 정합성 검증 (2.4(H) 결정 반영):
    - `CoreDiffOptions`에 `FileFilter` 필드를 추가하고 `ocr core diff`에 `--exclude` 플래그를 배선한다.
    - `coreWhyExcluded`의 판정 순서를 `internal/agent/preview.go`의 `whyExcluded`와 동일하게 맞춘다:
      `IsBinary` → `IsUserExcluded` → `HasInclude`/`IsUserIncluded` → 확장자 → `IsExcludedPath`.
    - 그 위에서 gitignore 부정 패턴·디렉터리 패턴·대소문자 무시가 `ocr core diff`와 `ocr review`에
      동일 적용되는지 동작 일치 테스트를 추가한다.
    - #609(index 헤더 제거)가 `ocr core diff` JSON의 `diff` 필드 출력을 바꾸는지 골든 테스트로 확인한다.
      바뀌면 Phase 5-16의 문서 예시 갱신 대상에 포함한다.
13. `internal/agent`, `internal/scan`, `internal/diff`, `internal/model`, `cmd/opencodereview` 테스트 일괄 실행.
14. 수동 스모크: `ocr core diff` / `relocate` / `emit` / `rule` / `prompt` 각 1회 실행, `ocr review --help`·shell completion 동작 확인.

### Phase 5 — 마무리

15. `skills/open-code-review-local/SKILL.md`가 Cobra 전환 후에도 유효한 명령 예시를 담고 있는지 점검 (명령 문법 변화 시 갱신).
16. `docs/ocr-core-local-usage.ko-KR.md`의 명령 예시 동기화.
17. 병합 커밋 push 전 전체 CI 시뮬레이션.

    **전제**: `.github/workflows/ci.yml`은 `push: branches: [main]`과 `pull_request: branches: [main]`에만
    반응하고 `runs-on: self-hosted`다. 즉 fork(`kall/open-code-review`)의 `feat/ocr-core-local`에 push해도
    CI는 돌지 않으며, 업스트림 PR을 열어야(그리고 메인테이너가 self-hosted 러너 실행을 승인해야) 발동한다.
    **로컬 시뮬레이션이 사실상 유일한 게이트이므로 목록을 CI와 정확히 일치시킨다.**

    ```bash
    make check   # license-check → english-check → go mod tidy → gofmt -s -w → go vet → test
    bash scripts/verify-action-pins.sh
    git ls-files --eol | grep -v 'i/lf\|i/none\|i/-text'   # 무출력이어야 함
    govulncheck ./...
    go test -race -count=1 -coverprofile=coverage.out ./... && go tool cover -func=coverage.out | grep total:
    for os in linux darwin windows; do for arch in amd64 arm64; do
      GOOS=$os GOARCH=$arch CGO_ENABLED=0 go build -o /dev/null ./... || echo "FAIL $os/$arch"
    done; done
    ```

    이어서 수동 스모크(Phase 4-14)를 마지막으로 수행한다.
    `govulncheck`와 `verify-action-pins.sh`는 네트워크가 필요하므로 오프라인 환경에서는 실행 불가를 기록해 둔다.

---

## 4. 리스크 및 판단 필요 사항

| 항목 | 리스크 | 대응 |
|---|---|---|
| Cobra 재작성 범위 | `flags.go` 삭제로 `runCoreXxx` 시그니처 보존이 불가 — 플래그 파싱·테스트까지 연쇄 변경 | 서브커맨드별 `cobra.Command` 분리 + 플래그 이관 + 테스트 갱신 (Phase 2-6) |
| 커버리지 90% | 기준선 90.3%로 여유가 0.3%p뿐 — 커버리지 0%인 `printCoreUsage`·수동 디스패처 처리에 따라 뒤집힘 | Phase 0에서 기준선 확정, Phase 3-11에서 90.3% 유지 확인 |
| core diff와 review diff의 파일 선정 불일치 | 실제 갭은 FileFilter 미배선 1건 — 나머지는 공용 로직 재사용으로 자동 반영 | FileFilter 배선 결정(2.4(H)) 후 Phase 4-12 동작 일치 테스트 |
| fork에 CI 미적용 | push 단계에서 게이트가 전혀 돌지 않아 누락이 업스트림 PR에서야 드러남 | Phase 5-17 로컬 시뮬레이션을 CI 스텝과 1:1로 유지 |
| 미추적 파일(`build.sh`, `next.md`, `ocr`, `package/`) | 병합과 무관하나, 추후 커밋 시 english-only·SPDX 게이트 대상 | 커밋 전 헤더·영어화 확인 |
