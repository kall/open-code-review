# 잔여 리뷰 지적 — `feat/ocr-core-local-merge-main`

- 리뷰 실행일: 2026-08-14
- 리뷰 대상: `277bdd6..b62c278` (origin/main 병합 이후 이 브랜치가 작성한 변경)
- 리뷰 구성: correctness, security, adversarial, testing, api-contract, project-standards, maintainability, agent-native (8인)
- 처리 결정: **수용하고 진행** — 아래 3건은 이번 병합이 만든 결함이 아니라 `ocr review`에도
  동일하게 존재하는 공유 코드 문제이므로 병합을 막지 않고 후속 작업으로 남긴다.

이번 리뷰에서 **적용 완료된** 지적은 커밋 `b62c278`에 있다. 아래는 의도적으로 남긴 항목이다.

---

## R1. 파일 필터 알고리즘이 세 벌로 복제되어 있다 (P1)

- 지적: maintainability(확신 100), adversarial(75), testing(75) — 3명 독립 지적
- 위치:
  - `internal/diff/core_diff.go` — `coreFilterReason` / `coreWhyExcluded`
  - `internal/agent/preview.go` — `(*Agent).whyExcluded`
  - `internal/scan/agent.go` — 세 번째 사본

동일한 판정 순서(binary → user exclude → user include 단락 → 확장자 허용목록 →
기본 제외경로 → deleted)가 세 곳에 손으로 복제되어 있다. 현재 패리티 테이블
(`internal/diff/core_filter_parity_test.go`, `internal/agent/whyexcluded_parity_test.go`)은
**앞의 두 개만** 감시하고, 그것도 서로를 비교하는 것이 아니라 각자 고정된 기대값만
확인한다. 따라서 한쪽 구현만 고치고 테이블에 케이스를 추가하지 않으면 두 명령의 파일
선정이 조용히 어긋나도 양쪽 테스트가 모두 통과한다. `internal/scan`의 세 번째 사본은
어느 테이블에도 없어 `ocr scan` 드리프트는 아예 무감시다.

**권장 해법**: `internal/config/rules`에 `func (f *FileFilter) WhyExcluded(path string, isBinary bool) model.ExcludeReason`를
추가하고 세 호출부가 모두 위임하게 한 뒤, 미러링된 두 테이블을 단일 테이블로 합친다.
`rules` 패키지는 stdlib과 doublestar만 import하므로 순환 의존이 생기지 않는다.

**이번에 하지 않은 이유**: `ocr review`와 `ocr scan`의 운영 필터 경로를 바꾸는 변경이라,
병합 복구 PR에 섞으면 리뷰 단위가 흐려지고 폭발 반경이 커진다. 별도 PR이 맞다.

**부분 완화(적용됨)**: 두 테이블에 중첩 디렉터리·리네임·대소문자 케이스를 추가해
실제로 갈릴 수 있는 축 일부를 감시 범위에 넣었다.

---

## R2. `--exclude` 도움말이 실제 문법을 잘못 설명한다 (P1)

- 지적: security(확신 100)
- 위치: `cmd/opencodereview/shared_flags.go` — `addExcludeFlag`

도움말은 "comma-separated gitignore-style patterns"라고 하지만 실제 매칭은 전체 경로를
대상으로 하는 doublestar glob이다. 그래서 `secrets/`나 `*.tfvars` 같은 gitignore식 표기는
조용히 아무것도 걸러내지 않고, 사용자는 제외했다고 믿는다. 매칭되지 않은 패턴에 대한
경고도 없다.

**권장 해법**: 문구를 실제 동작에 맞게 고치고(`'*' does not cross '/'` 명시), 어떤 변경
파일에도 매칭되지 않은 `--exclude` 패턴을 stderr에 경고로 출력한다.

**이번에 하지 않은 이유**: 이 헬퍼는 `review`·`scan`·`delegate`가 함께 쓰므로 문구를 바꾸면
네 명령의 도움말이 동시에 바뀐다. 대신 이번 PR에서는 스킬 문서와 한국어 가이드,
그리고 `ocr core diff`의 Example에 정확한 패턴과 주의사항을 적어 사용자 위험을 낮췄다.

---

## R3. `--rule`은 프로젝트 `rule.json`의 exclude를 병합하지 않고 대체한다 (P1)

- 지적: security(확신 100)
- 위치: `internal/config/rules/system_rules.go` — `buildFileFilter`

`buildFileFilter`는 include/exclude 중 하나라도 정의한 **첫 레이어**에서 곧바로 반환한다
(custom → project → global 순서). 따라서 `--rule custom.json`을 주면 프로젝트
`.opencodereview/rule.json`의 exclude가 더해지는 게 아니라 통째로 버려진다.

**권장 해법**: 병합 시맨틱을 택한다면 exclude는 전 레이어 합집합, include는 최고 우선
레이어만 채택하도록 바꾸고 `ocr review`/`ocr scan` 회귀 테스트를 추가한다.

**이번에 하지 않은 이유**: 업스트림 동작이며 `ocr review`도 동일하다. 즉 `ocr core diff`는
패리티상 **정확하다** — 여기서 core만 바꾸면 오히려 두 명령이 어긋난다. 동작 변경은
업스트림 결정 사항이므로 이번에는 문서에 경고만 추가했다.

---

## 참고: 지적됐으나 조치 불필요로 판단한 항목

- `ocr core diff` / `relocate` / `emit`이 `cobra.NoArgs`로 바뀌면서 잉여 위치 인자를
  거부한다(api-contract, P3, advisory). 이는 저장소가 #749/#892에서 도입한 컨벤션을
  따른 의도된 동작이다.
- 패키지 전역 `coreDiffOpts`는 `review_cmd.go`의 `reviewOpts` 관례와 동일하다. 테스트
  누수 위험은 이번에 `execRoot`의 help 플래그 리셋과 `t.Cleanup` 복원으로 막았다.
