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
| codex-windows-sandbox | fix/codex-windows-sandbox | Codex Windows sandbox policy | git revert --grep='topic: codex-windows-sandbox' |

## Известные конфликты слияния

- rule-groups x droid-integration пересекаются по Go (droid.go, daemon/*.go, handler/agent.go).
  В droid.go берётся версия droid-integration (temp-file, фиксит too long path).
  В types.go/execenv.go/agent.go/daemon.go поля ОБЪЕДИНЯЮТСЯ (ProjectEnvironments + EffectiveRules).
- ru-locale add/add решается резолвером prefer-ours + append-theirs.

## Сборка

Сборка идёт прямо с `main` (без промежуточной integration-ветки). Логика
синхронизации, локализации и docker-build описана в промпте автопилота
«Обновление Multica» — отдельных скриптов в репозитории нет.
