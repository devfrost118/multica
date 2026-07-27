# LOCAL_FEATURES.md - локальные доработки поверх upstream/main

Этот файл - легенда локальных (не в апстриме) функциональностей.
Живёт только в твоём форке, НЕ пушится в апстрим.

## Принцип (вариант А: topic-merge в main)

- main = upstream/main (latest release tag vX.Y.Z) плюс тематические мерджи.
- Каждая локальная функциональность вливается через:
  git merge --no-ff feat/<x> -m "topic: <name> - ..."
- Метка topic: в сообщении мерджа - единственный источник истины.
- Вырезать функциональность, когда она появилась в апстриме:
  git revert --no-commit $(git log --grep='topic: <name>' --format=%H) && git commit -m "revert: drop local <name> (now upstream)"

## Активные локальные доработки

| Topic | Источник | FRO / Назначение | Вырезка |
|-------|----------|------------------|---------|
| project-environments | feat/project-environments-epic | FRO-138: env storage/API/daemon/UI/secrets | git revert --grep='topic: project-environments' |
| provider-limits | feat/provider-limits-epic | FRO-160: provider-limit collector/adapters/UI | git revert --grep='topic: provider-limits' |
| droid-integration | feat/droid-integration | FRO-184: QA Droid (temp-file system prompt, фикс Windows argv limit) | git revert --grep='topic: droid-integration' |
| i18n-russian | feat/i18n-russian | RU locale parity | git revert --grep='topic: i18n-russian' |
| rule-groups | feat/rule-groups-epic | FRO-174: rule groups | git revert --grep='topic: rule-groups' |
| stale-issue-inbox-refetch | fix/stale-issue-inbox-refetch | re-fetch inbox перед main rebase | git revert --grep='topic: stale-issue-inbox-refetch' |
| docker-build-context-ignore-node-modules | fix/docker-build-context-ignore-node-modules | .dockerignore node_modules | git revert --grep='topic: docker-build-context-ignore-node-modules' |

Снятые темы (поглощены апстримом):

- **codex-windows-sandbox** — выведена из эксплуатации при синке v0.4.6→v0.4.7: апстрим закрыл ту же проблему нативно (MUL-4957, `codex_sandbox.go`: per-task `windows.sandbox` opt-in, `resolveGOOS`, интеграционные тесты — строгое надмножество нашего безусловного fallback). Локальная тема (`f2ae31ca3`) и приблудный `7fdfefbee` из ветки rule-groups отревертнуты.

## Известные конфликты слияния

- rule-groups x droid-integration пересекаются по Go (droid.go, daemon/*.go, handler/agent.go).
  В droid.go берётся версия droid-integration (temp-file, фиксит too long path).
  В types.go/execenv.go/agent.go/daemon.go поля ОБЪЕДИНЯЮТСЯ (ProjectEnvironments + EffectiveRules).
- ru-locale add/add решается резолвером prefer-ours + append-theirs.

## Сборка

Сборка идёт прямо с `main` (без промежуточной integration-ветки). Логика
синхронизации, локализации и docker-build описана в промпте автопилота
«Обновление Multica» — отдельных скриптов в репозитории нет.

## Журнал синков

### 2026-07-27: v0.4.7 → v0.4.12 (FRO-199)

- Все 7 локальных тем целы, ничего не снималось. 108 upstream-коммитов, 718 файлов, 28 новых миграций.
- Pre-update backup: `E:\backups\multica\pre-update\20260727T151230Z\` (`multica.dump` 17 435 933 B, `.env` 9 761 B, `manifest.txt` с SHA-256). Содержимое `.env` нигде не печаталось и не коммитилось.
- **Грабли шага 5 (важно для следующего синка):** локальный `main` отставал от `origin/main` на 2 коммита (PR #16 FRO-197, #17 FRO-196). Процедура «merge тега → `push --force-with-lease`» в этом состоянии **проходит** lease-проверку (remote-tracking ref свежий после `fetch`) и перезаписывает `origin/main`, теряя оба PR. Правильный порядок: `git merge --ff-only origin/main` **до** merge тега; после этого push — обычный fast-forward, force не нужен.
- Конфликты: 14 хунков в 12 файлах, разрешены объединением (`ProjectEnvironments` + `EffectiveRules` в `types.go`/`execenv.go`/`handler/agent.go`; `AllowNoAgents` + `droid` в `config.go`; `ProjectEnvironmentSecrets`/`ProviderCredentials` рядом с апстримными `VCSSecretBox`/`PRRefresh` в `handler.go`).
- **Семантические (не текстовые) конфликты** — merge прошёл чисто, но сборка падала:
  1. `handler/skill_discover.go` (локальный файл) звал GitHub-хелперы по старым сигнатурам — апстрим добавил первым параметром `context.Context`. Пробросил `r.Context()`.
  2. `app-sidebar.tsx`: апстрим убрал иконки из `configureNav` (теперь их даёт `routeIconForPath`). Локальный пункт `rules` пришлось зарегистрировать в реестре: `packages/core/paths/route-icons.ts` (+`ShieldCheck`, +`rules`) и `packages/views/layout/route-icon-components.tsx`.
- Нумерация миграций: локальные 203–208 и апстримные 203–208 совпадают по префиксу, но это **не** проблема — раннер (`server/cmd/migrate/main.go`) хранит в `schema_migrations.version` полное имя файла, а не число. Применилось 250 → 282.
- Локализация RU: +130 ключей, −68 устаревших (`node locale-gaps.mjs ru --prune`). `parity.test.ts` — 212 passed; ja/ko/zh-Hans тоже без пробелов.
- Проверки: `go build ./...` OK, `pnpm typecheck` 6/6 OK, backend health HTTP 200, миграции `Done.` без ошибок.
- Замечено: апстрим перестал публиковать порт postgres на хост (было `127.0.0.1:5432->5432/tcp`, стало только `5432/tcp` внутри сети). Если что-то ходило в БД с хоста — учесть.
- Своп демона: `server/bin/multica.exe.new` собран (29 764 096 B, `v0.4.12-101-g51c829995`), скрипт `server/bin/daemon-swap.ps1` готов, **но задача Планировщика не зарегистрирована** — в песочнице агента `schtasks.exe` и `Register-ScheduledTask` недоступны (`Access is denied` / `EPERM`). Своп нужно запустить вручную, см. отчёт в FRO-199.

### 2026-07-21: v0.4.6 → v0.4.7

- `codex-windows-sandbox` снята (см. таблицу выше); остальные 7 тем целы.
- **Найдено и починено при синке (не относится к v0.4.7 как таковому):**
  - Мердж `rule-groups` (528259339) был разрешён с ошибками ещё до этого синка — `main` не собирался (`go build`) из-за задвоенных блоков полей в `types.go`/`execenv.go`/`handler/agent.go` и оборванного литерала в `daemon.go`. Тот же паттерн — в `packages/core/api/client.ts` (задвоенный блок Labels с неверным `scope=` вместо `resource_type=`, плюс потерянные импорты `RuleGroup*Schema`/`EMPTY_*` из `./schemas`). Всё починено.
  - Миграция `180_provider_limit_snapshots` была отредактирована задним числом (`f1194180c` добавил `daemon_id` в уже применённый `CREATE TABLE`) — на инсталляциях, где 180 уже накатилась, колонки не было. Компенсирующая `203_provider_limit_snapshots_daemon_id_backfill` (`ADD COLUMN IF NOT EXISTS`) вставлена перед индексом; апстримная и droid-миграции сдвинуты на 204/205.
  - 91 «осиротевший» ru-ключ (без en-пары, в основном из старого UI автопилотов) убраны — `locales/parity.test.ts` требует строгого двустороннего соответствия без prune-шага.
- Своп демона: PID 10380 → PID 25108, удался с 3-й попытки (см. `daemon-swap.log` и `~/.multica/profiles/desktop-localhost-8080/daemon.log`). Грабли на будущее:
  1. Задача Планировщика Windows не наследует interactive PATH — вызывай `multica.exe` по абсолютному пути, не по имени.
  2. `2>&1` на нативном вызове внутри PowerShell с `$ErrorActionPreference = "Stop"` заворачивает строку stderr в терминирующее исключение (даже при exit code 0) — не редиректь stderr вызовов `multica.exe`, читай его отдельно или гаси `2>$null`.
  3. Живой демон работает не в дефолтном (безымянном) профиле, а в `--profile desktop-localhost-8080` (`~/.multica/profiles/desktop-localhost-8080/config.json`, реальный токен `mul_...`). `multica daemon stop/start/status` без `--profile` бьёт по другому, почти неиспользуемому профилю с 7-символьным «токеном» и молча не трогает боевой процесс (`daemon status` там даже врёт «stopped», пока боевой демон жив под своим PID). Следующий своп: сразу используй `--profile desktop-localhost-8080`.
  4. Убийство демона посреди активной задачи может заставить новый демон при старте «доподхватить» и зарезюмировать эту же задачу вторым процессом — ожидаемо, не паниковать, не убивать руками.
