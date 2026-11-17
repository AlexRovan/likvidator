# likvidator

Kubernetes-оператор, который удаляет устаревшие **Helm-релизы** по правилам очистки.

Полная спецификация: [docs/SPEC.md](docs/SPEC.md).

## Как это работает

Создаёте namespaced CR `Cleaner` (`apps.altech.com/v1`). Контроллер периодически:

1. Выбирает Namespace по `spec.namespaceSelector` (пустой селектор → только namespace самого `Cleaner`).
2. Ищет Pod с лейблом-гейтом (`cleaner` по умолчанию).
3. Группирует их по лейблу `release` (имя Helm-релиза).
4. Оценивает OR-условия и при необходимости делает uninstall (или только пишет status в `dryRun`).

**Без лейбла `cleaner` релиз не удаляется.**

### Делегирование uninstall (RBAC)

Discovery (Pods / Namespaces / чтение Secrets) — cluster-wide у manager SA.  
**Delete / Helm uninstall** — только если в целевом namespace есть RoleBinding на `likvidator-release-cleaner-role` (в т.ч. PDB).

```sh
# Подставьте namespace релиза (например vika)
sed 's/REPLACE_WITH_TARGET_NAMESPACE/vika/' \
  config/samples/apps_v1_release_cleaner_rolebinding.yaml | kubectl apply -f -
```

Без RoleBinding: `dryRun` / candidates работают; реальный uninstall даст Forbidden и Condition `Degraded` (`UninstallForbidden`).

### Условия удаления (OR)

| Причина | Когда |
|---------|--------|
| `Unhealthy` | У Pod есть `CrashLoopBackOff` / `ImagePullBackOff` непрерывно ≥ `unhealthyDuration` (default `48h`) |
| `JiraClosed` | Тег образа `PROJ-123-abc12` → issue `PROJ-123` закрыта в JIRA ≥ `jiraGracePeriod` (default `24h`) |
| `StaleRelease` | Helm `info.last_deployed` старше `staleReleaseDuration` (default `336h` = 14 дней) |

### Status

После скана у `Cleaner` видно:

- `observedReleases` — все наблюдаемые релизы
- `candidates` — eligible к удалению
- `deleted` — удалены или «would delete» при dry-run
- `releaseStates` — таймеры между reconcile
- `conditions` — `Ready` / `Degraded` / `Progressing`

## JIRA Secret

Secret в **том же namespace**, что и `Cleaner` (`spec.jira.secretRef.name`):

| Ключ | Обязательный | Описание |
|------|--------------|----------|
| `url` | да | Base URL, например `https://jira.example.com` |
| `apiToken` | да | API token / пароль |
| `username` | нет | Если задан — HTTP Basic; иначе Bearer `apiToken` |

Пример:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: jira-credentials
  namespace: likvidator-system
type: Opaque
stringData:
  url: "https://jira.example.com"
  username: "likvidator-bot"
  apiToken: "REPLACE_ME"
```

## Пример Cleaner

Sample: [config/samples/apps_v1_cleaner.yaml](config/samples/apps_v1_cleaner.yaml) (`dryRun: true`).

```yaml
apiVersion: apps.altech.com/v1
kind: Cleaner
metadata:
  name: cluster-cleaner
  namespace: likvidator-system
spec:
  dryRun: true
  interval: 15m
  namespaceSelector:
    matchLabels:
      environment: dev
  cleanerLabelKey: cleaner
  releaseLabelKey: release
  unhealthyDuration: 48h
  jiraGracePeriod: 24h
  staleReleaseDuration: 336h
  jira:
    secretRef:
      name: jira-credentials
```

Pod-кандидат:

```yaml
metadata:
  labels:
    cleaner: "true"
    release: demo-app
spec:
  containers:
    - image: registry.example.com/demo:PROJ-123-abc12
```

## Getting Started

### Prerequisites

- Go 1.26+
- Docker 17.03+
- kubectl
- Kubernetes cluster (для deploy / e2e)

### Deploy

```sh
make docker-build docker-push IMG=<registry>/likvidator:tag
make install
make deploy IMG=<registry>/likvidator:tag
kubectl create namespace likvidator-system   # если ещё нет
kubectl apply -k config/samples/
```

Перед продом замените `apiToken` в Secret и при необходимости выставьте `dryRun: false`.

### Uninstall

```sh
kubectl delete -k config/samples/
make undeploy
make uninstall
```

### Bundle

```sh
make build-installer IMG=<registry>/likvidator:tag
kubectl apply -f dist/install.yaml
```

## Development / Tests

```sh
# unit (policy, podutil, jira, helm) + controller envtest
make test

# только пакеты без envtest
go test ./internal/policy/ ./internal/podutil/ ./internal/jira/ ./internal/helm/

make run   # локально против текущего kubeconfig
```

`make test` скачивает envtest binaries (`setup-envtest`). Нужен доступ к GitHub releases.

### Покрытие controller (envtest)

В `internal/controller/cleaner_controller_test.go`:

- пустой скан без labeled pods
- stale release + `dryRun: true` → candidate/deleted без uninstall
- pod без лейбла `cleaner` → не наблюдается
- unhealthy ≥ 48h + `dryRun: false` → mock Helm uninstall

## Project layout

```text
api/v1/                 Cleaner CRD types
internal/controller/    Reconcile
internal/helm/          Get last_deployed + Uninstall (secret storage)
internal/jira/          Secret loader + REST client
internal/policy/        OR rules + image tag parse
internal/podutil/       CrashLoop / ImagePull detect
config/rbac/            manager ClusterRole + release-cleaner (RoleBinding per ns)
config/samples/         Cleaner, Secret, RoleBinding example
docs/SPEC.md            спецификация
```

## License

Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
