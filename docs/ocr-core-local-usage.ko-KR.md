# ocr core + 무(無) API key 코드리뷰 사용법

Claude Code CLI 안에서 **별도 LLM API key 없이** 코드리뷰를 돌리는 방법입니다.
대표 사용 사례: Claude Code에게 **"glab !123 MR 리뷰해줘"** 라고 하면 MR diff를
가져와 라인 단위로 리뷰합니다.

## 동작 원리 (왜 API key가 필요 없나)

- **`ocr core`** (결정론 기계, 신규): diff 파싱·라인 재배치·룰/프롬프트 출력·결과
  포맷팅만 수행하고 **LLM/네트워크 호출이 0**입니다. 자격증명이 전혀 필요 없습니다.
- **Claude Code** (두뇌): 추론(코드 이해·코멘트 생성)은 Claude Code가 **당신의 구독**
  으로 수행합니다. 그래서 `ANTHROPIC_API_KEY` 없이 동작합니다.
- 기존 `ocr review`(LLM 바이너리 경로)와 `internal/llm`은 그대로 유지됩니다(additive).

> 키가 있고 `ocr review`를 그대로 쓰고 싶다면 기존 `open-code-review` 스킬을 쓰세요.
> 이 문서는 **키 없이** 구독으로 돌리는 `open-code-review-local` 경로입니다.

---

## 1. 설정

### 1-1. `ocr` 빌드 (core 명령 포함)

`ocr core` 명령군이 포함된 바이너리가 필요합니다. 저장소에서 빌드합니다.

```bash
# 저장소 루트에서
make build                      # bin 또는 ./opencodereview 생성
# 또는
go build -o ocr ./cmd/opencodereview
```

> 이 머신처럼 go가 PATH에 없고 mise로 관리되면:
> `mise exec go@1.26.4 -- go build -o ocr ./cmd/opencodereview`

빌드한 `ocr`를 PATH에 둡니다(예: `~/bin`, `/usr/local/bin`). 확인:

```bash
ocr core -h        # 서브커맨드 목록이 나오면 OK
```

`ocr core`가 없다고 나오면 core 명령군 이전 버전이므로 위에서 다시 빌드하세요.

### 1-2. Claude Code 구독 로그인 (키 설정 안 함)

- Claude Code가 **Pro/Max 구독(OAuth)** 으로 로그인돼 있어야 합니다.
- 이 경로에서는 `ANTHROPIC_API_KEY`/`OCR_LLM_*`를 설정하지 **마세요**. 추론은 구독으로
  이뤄집니다.

### 1-3. glab 인증 (GitLab MR 리뷰용)

```bash
glab auth status     # 인증돼 있는지 확인 (안 돼 있으면 glab auth login)
```

> 이 머신에서는 `mise exec -- glab ...` 형태로 실행됩니다.

GitHub PR을 리뷰하려면 `gh auth status`로 `gh`가 인증돼 있어야 합니다.

### 1-4. 스킬을 Claude Code가 인식하게 하기

`skills/open-code-review-local/SKILL.md`를 Claude Code가 찾을 수 있는 위치에 둡니다.

```bash
# 사용자 전역 스킬로 설치
mkdir -p ~/.claude/skills/open-code-review-local
cp skills/open-code-review-local/SKILL.md ~/.claude/skills/open-code-review-local/
```

또는 이 저장소 안에서 작업한다면 Claude Code가 저장소의 `skills/`를 자동 인식할 수
있습니다(환경에 따라 다름). 인식 여부는 Claude Code에게 "open-code-review-local 스킬
있어?" 라고 물어 확인하면 됩니다.

---

## 2. 사용법

### 2-1. GitLab MR 리뷰 (대표 사용 사례)

리뷰하려는 MR이 있는 저장소 디렉터리에서, Claude Code에게 자연어로 요청합니다.

```
glab !123 MR 리뷰해줘
```

그러면 스킬이 다음을 수행합니다.

1. `glab mr checkout 123` — MR 소스 브랜치를 로컬로 가져와 체크아웃
2. `glab mr view 123` — 타깃 브랜치(보통 `main`) 확인
3. `ocr core diff --repo . --from <타깃> --to <MR소스브랜치>` — **MR 변경분만** JSON으로
   추출 (테스트·문서·대용량 파일은 자동 제외)
4. 파일마다: `ocr core rule <path>`(룰) + `ocr core prompt main`(프롬프트) 주입 →
   Claude Code가 네이티브 `Read`/`Grep`/`Glob`로 컨텍스트 수집 → 코멘트 생성
5. 코멘트마다 `ocr core relocate`로 정확한 라인 확정
6. 우선순위(High/Medium)로 묶어 리포트. 필요하면 `ocr core emit`으로 기존 CI 호환 JSON 출력

> "리뷰하고 고쳐줘"라고 하면 High/Medium 항목을 직접 수정까지 합니다(커밋 전 확인).

### 2-2. 그 외 리뷰 대상

| 요청 예 | 동작 |
|---|---|
| "지금 변경분 리뷰해줘" | 워크스페이스(staged+unstaged+untracked) 리뷰 |
| "이 브랜치 리뷰해줘" | `--from <base> --to <branch>` |
| "커밋 abc123 리뷰해줘" | `--commit abc123` |
| "GitHub PR 45 리뷰해줘" | `gh pr checkout 45` → `--from <base> --to HEAD` |

### 2-3. 코어 명령 직접 사용 (수동/CI)

스킬 없이 `ocr core`를 직접 쓸 수도 있습니다.

```bash
# 리뷰 대상 diff(본문+hunk맵+제외사유) JSON
ocr core diff --repo . --from main --to my-feature

# 사용자 include/exclude 규칙 적용 (ocr review와 동일한 파일 선정)
ocr core diff --repo . --exclude '**/generated/**,**/vendor/**'
ocr core diff --repo . --rule ./custom-rule.json

# 특정 파일에 적용되는 리뷰 룰
ocr core rule src/main/java/com/example/Foo.java

# 페이즈 프롬프트(main|plan|filter|relocation) — JSON [{role,content},...]
ocr core prompt main

# 코멘트의 existing_code → 정확한 라인 (stdin JSON)
echo '{"diff":"...","new_file_content":"...","comment":{"content":"...","existing_code":"..."}}' \
  | ocr core relocate

# 코멘트 배열 → 기존 ocr review JSON 계약으로 래핑 (CI 무변경 소비)
echo '[{"path":"a.go","content":"nit","start_line":3,"end_line":3}]' | ocr core emit
```

`ocr core`의 모든 서브커맨드는 LLM/네트워크 호출이 없습니다.

---

## 3. 한계

- **exclude 패턴은 `.gitignore` 문법이 아니라 전체 경로 대상 doublestar glob.**
  `*`는 `/`를 넘지 않으므로 `**/vendor/*`는 `vendor/pkg.go`만 걸러내고
  `vendor/x/y/pkg.go`는 리뷰 대상으로 남깁니다. 디렉터리 전체를 빼려면 `/**`를 쓰세요.
  `secrets/`나 `*.tfvars` 같은 gitignore식 표기는 기대대로 동작하지 않습니다.
- **`--rule`은 병합이 아니라 대체.** include/exclude는 custom(`--rule`) →
  프로젝트(`.opencodereview/rule.json`) → 전역 순서에서 **둘 중 하나라도 정의한 첫 레이어**만
  채택됩니다. 따라서 `--rule`을 주면 프로젝트 파일의 exclude는 더해지는 게 아니라 버려집니다
  (`ocr review`도 동일). CLI `--exclude`는 그렇게 선택된 레이어 위에 덧붙습니다.
- **`include` 패턴은 기본 필터를 건너뜁니다.** include에 매칭된 파일은 확장자 허용목록과
  기본 제외경로를 통과하므로, `src/**` 같은 넓은 include는 원래 걸러지던 비소스 파일까지
  리뷰 대상으로 올립니다.
- **relocate는 new-file 코드를 기대.** `existing_code`는 추가(`+`)·문맥 라인에서
  뽑으세요. 삭제(`-`) 라인 스니펫은 잘못된 라인으로 매핑될 수 있습니다.
- **`ocr core prompt`는 원본 템플릿을 그대로 출력**합니다(`{{diff}}` 등 플레이스홀더
  미치환). 스킬이 채우거나 구조 지침으로 사용합니다.
- **패리티 미검증(U6).** 스킬은 PLAN(변경 50줄↑ 리스크 분석)·파일별 병렬 리뷰·
  결정론+LLM relocate 폴백·opt-in REVIEW_FILTER(격리된 오탐 제거)까지 수행합니다.
  다만 `ocr review` 대비 Precision 벤치마크(U6)는 아직 수행하지 않았으므로, 결과는
  검증된 동등물이 아니라 best-effort로 취급하세요.
- **구독 사용량 한도 적용.** 큰 MR이나 넓은 병렬은 구독 한도를 빠르게 소진할 수
  있습니다. 동시성은 적당히(≈3–5) 두고 소~중 규모 diff를 권장하며, 로컬 인터랙티브
  사용을 전제로 합니다(무인 CI는 약관 확인 후).

---

## 4. 트러블슈팅

| 증상 | 원인 / 해결 |
|---|---|
| `ocr core` not found | core 미포함 바이너리. 1-1로 재빌드 후 PATH 확인 |
| diff가 비어 있음 | `--from/--to` ref가 로컬에 없음. `glab mr checkout`/`git fetch` 먼저 |
| MR 파일이 리뷰에서 빠짐 | 확장자 허용목록 밖이거나 `*_test.go` 등 기본 제외. `exclude_reason` 확인 (`user_exclude`면 rule.json 또는 `--exclude` 패턴) |
| 키를 요구함 | 이 경로는 키 불요. `ocr review`(LLM 경로)를 부르고 있지 않은지 확인 |
| 데이터 우려 | 리뷰 시 소스/diff가 Claude Code(구독)로 전송됨. 기밀/NDA 코드는 조직 정책 확인 |

---

## 참고

- 결정론 코어 구현: `internal/diff/core_diff.go`, `cmd/opencodereview/core_cmd.go`
- 스킬: `skills/open-code-review-local/SKILL.md`
- 설계 기록: `docs/brainstorms/2026-06-23-ocr-core-local-claude-code-requirements.md`,
  `docs/plans/2026-06-27-001-feat-ocr-core-local-claude-code-plan.md`
- LLM 바이너리 경로(키 필요): 기존 `open-code-review` 스킬 / `ocr review`
