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

### 2026-07-21: v0.4.6 → v0.4.7

- `codex-windows-sandbox` снята (см. таблицу выше); остальные 7 тем целы.
- **Найдено и починено при синке (не относится к v0.4.7 как таковому):**
  - Мердж `rule-groups` (528259339) был разрешён с ошибками ещё до этого синка — `main` не собирался (`go build`) из-за задвоенных блоков полей в `types.go`/`execenv.go`/`handler/agent.go` и оборванного литерала в `daemon.go`. Тот же паттерн — в `packages/core/api/client.ts` (задвоенный блок Labels с неверным `scope=` вместо `resource_type=`, плюс потерянные импорты `RuleGroup*Schema`/`EMPTY_*` из `./schemas`). Всё починено.
  - Миграция `180_provider_limit_snapshots` была отредактирована задним числом (`f1194180c` добавил `daemon_id` в уже применённый `CREATE TABLE`) — на инсталляциях, где 180 уже накатилась, колонки не было. Компенсирующая `203_provider_limit_snapshots_daemon_id_backfill` (`ADD COLUMN IF NOT EXISTS`) вставлена перед индексом; апстримная и droid-миграции сдвинуты на 204/205.
  - 91 «осиротевший» ru-ключ (без en-пары, в основном из старого UI автопилотов) убраны — `locales/parity.test.ts` требует строгого двустороннего соответствия без prune-шага.
- Своп демона: удался с 3-й попытки, см. `daemon-swap.log`. Грабли на будущее:
  1. Задача Планировщика Windows не наследует interactive PATH — вызывай `multica.exe` по абсолютному пути, не по имени.
  2. `2>&1` на нативном вызове + `$ErrorActionPreference="Stop"` в PowerShell 5.1 превращает строку stderr в завершающее исключение — не редиректь stderr так в скриптах свопа.
  3. **Живой демон работает под именованным профилём** (`multica --profile desktop-localhost-8080 daemon ...`), не под дефолтным (без `--profile`) — у дефолтного профиля свой отдельный (нерабочий) токен. `multica daemon stop/start/status` без `--profile` молча бьёт не в тот демон. PID живого демона надёжнее всего смотреть через `Get-Process multica`, а профиль — через `~/.multica/profiles/*/daemon.pid`.
  4. Убийство демона посреди активной задачи может заставить новый демон при старте «доподхватить» и зарезюмировать эту же задачу вторым процессом — ожидаемо, не паниковать, не убивать руками.
