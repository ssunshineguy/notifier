# Notifier

Notifier отправляет клиенту HTTP-уведомление, когда выполняются заданные клиентом условия (`notification_params`): элементы верхнего уровня объединяются по «И», для других комбинаций есть группы `and` и `or` с вложенностью до трёх уровней. API сохраняет параметры, а Worker проверяет условия и доставляет результат асинхронно.

## Целевая архитектура

![Целевая схема Notifier с двумя Information Services](docs/architecture.png)

На схеме показаны два источника данных — Information Service 1 и Information Service 2. Worker может опрашивать и дополнительные источники.

### За рамками архитектурной схемы

- Антиробот для защиты от DDoS.
- Обязательный прокси Squid для выхода во внешний интернет.

## Компоненты

| Компонент | За что отвечает |
| --- | --- |
| Balancer | Принимает входящий трафик. |
| RPS Limiter | Ограничивает частоту запросов. |
| API Gateway | Принимает входящий трафик. Занимается оркестрацией запросов, а также подготовкой запросов извне к работе с внутренними сервисами. Отправляет OAuth на проверку в Auth Service и передаёт в Notifier `X-Client-ID` и `X-Request-ID`. |
| Auth Service | Проверяет авторизацию и аутентификацию клиента. В случае успеха обменивает credentials на `client_id` — внутренний идентификатор клиента. |
| Notifier API | Создаёт, читает, изменяет и удаляет нотификации по запросу пользователя. |
| PostgreSQL | Хранит данные о нотификациях. |
| Notifier Worker | С заданной периодичностью проверяет условия отправки нотификаций, а также занимается их отправкой. |
| Notifier Collector | Пакетно удаляет остановленные нотификации. |
| Directory | Распространяет справочные данные по сервисам платформы. В целевой схеме потребители получают их в память. |

**Доставка рассчитана на at-least-once.** Клиент может получить повторный вызов с тем же `notification_id`, например если обработал уведомление, а Worker не получил ответ. При исчерпании повторов задача завершается с ошибкой доставки.

## Диаграммы последовательности

### API

<details>
<summary>Create</summary>

```mermaid
sequenceDiagram
    participant C as Client
    participant G as API Gateway
    participant A as Auth Service
    participant N as Notifier API
    participant DB as PostgreSQL

    C->>G: Create<br/>OAuth, X-Request-ID<br/>Тело params с обязательным external_id
    G->>A: Проверить OAuth<br/>X-Request-ID
    A-->>G: client_id
    G->>G: Удалить OAuth и установить X-Client-ID = client_id
    G->>N: Create<br/>X-Client-ID, X-Request-ID<br/>Тело params
    N->>N: Сгенерировать notification_id
    N->>DB: INSERT INTO t_notification<br/>external_id, id = notification_id, data = params (JSONB)<br/>client_id = X-Client-ID, deadline_dt, start_dt = now()<br/>next_scheduled_dt = now() + initial_check_delay
    DB-->>N: Запись создана
    N-->>G: 200 OK, {notification_id}
    G-->>C: 200 OK, {notification_id}
```

</details>

<details>
<summary>Read</summary>

```mermaid
sequenceDiagram
    participant C as Client
    participant G as API Gateway
    participant A as Auth Service
    participant N as Notifier API
    participant DB as PostgreSQL

    C->>G: Read notification_id<br/>OAuth, X-Request-ID
    G->>A: Проверить OAuth<br/>X-Request-ID
    A-->>G: client_id
    G->>G: Удалить OAuth и установить X-Client-ID = client_id
    G->>N: Read notification_id<br/>X-Client-ID, X-Request-ID
    N->>DB: SELECT data, deadline_dt, external_id, start_dt<br/>FROM t_notification WHERE id = notification_id<br/>AND client_id = X-Client-ID
    DB-->>N: data (JSONB), deadline_dt, external_id, start_dt
    N-->>G: 200 OK, {data, deadline_dt, external_id, start_dt}
    G-->>C: 200 OK, {data, deadline_dt, external_id, start_dt}
```

</details>

<details>
<summary>Update</summary>

```mermaid
sequenceDiagram
    participant C as Client
    participant G as API Gateway
    participant A as Auth Service
    participant N as Notifier API
    participant DB as PostgreSQL

    C->>G: Update notification_id<br/>OAuth, X-Request-ID<br/>Тело: только новые notification_params
    G->>A: Проверить OAuth<br/>X-Request-ID
    A-->>G: client_id
    G->>G: Удалить OAuth и установить X-Client-ID = client_id
    G->>N: Update notification_id<br/>X-Client-ID, X-Request-ID<br/>Тело params
    N->>DB: UPDATE t_notification SET data = params (JSONB)<br/>WHERE id = notification_id<br/>AND client_id = X-Client-ID AND stop_dt IS NULL
    DB-->>N: Запись обновлена
    N-->>G: 200 OK
    G-->>C: 200 OK
```

</details>

<details>
<summary>Delete</summary>

```mermaid
sequenceDiagram
    participant C as Client
    participant G as API Gateway
    participant A as Auth Service
    participant N as Notifier API
    participant DB as PostgreSQL

    C->>G: Delete notification_id<br/>OAuth, X-Request-ID
    G->>A: Проверить OAuth<br/>X-Request-ID
    A-->>G: client_id
    G->>G: Удалить OAuth и установить X-Client-ID = client_id
    G->>N: Delete notification_id<br/>X-Client-ID, X-Request-ID
    N->>DB: DELETE FROM t_notification<br/>WHERE id = notification_id AND client_id = X-Client-ID<br/>RETURNING id
    DB-->>N: ID удалённой записи
    N-->>G: 200 OK
    G-->>C: 200 OK
```

</details>

### Фоновые процессы

<details>
<summary>Worker</summary>

```mermaid
sequenceDiagram
    participant W as Notifier Worker
    participant DB as PostgreSQL
    participant G as Worker Goroutine
    participant C as Client
    participant I1 as Information Service 1
    participant I2 as Information Service 2

    W->>DB: BEGIN
    W->>DB: SELECT id FROM t_notification<br/>WHERE next_scheduled_dt <= now() AND stop_dt IS NULL<br/>AND (lock_until_dt IS NULL OR lock_until_dt <= now())<br/>LIMIT число свободных слотов<br/>FOR UPDATE SKIP LOCKED
    DB-->>W: ID выбранных записей
    opt Есть выбранные записи
        W->>DB: UPDATE выбранных записей<br/>SET lock_until_dt = now() + lock_duration<br/>RETURNING записи с lock_until_dt
        DB-->>W: Пачка записей с выданным lock_until_dt
    end
    W->>DB: COMMIT
    DB-->>W: Транзакция зафиксирована

    opt Пачка непустая
        loop Для каждой записи в пачке
            W-)G: Передать запись и выданный lock_until_dt
        end

        Note over G: Обработка одной записи в отдельной горутине
        alt Повтор отправки, failed_attempt_count > 0
            G->>G: Если deadline_dt <= now(), notification_status = 'deadline_exceeded'<br/>Иначе notification_status = 'success'
            G->>C: Повторный Notify<br/>notification_id, notification_status
            C-->>G: HTTP-ответ, если получен
        else Проверка условия отправки
            par Information Service 1
                G->>I1: Запросить данные о событии
                I1-->>G: Данные о событии
            and Information Service 2
                G->>I2: Запросить данные о событии
                I2-->>G: Данные о событии
            end
            G->>G: Проверить условие отправки

            alt Событие наступило
                G->>C: Notify о событии<br/>notification_id, notification_status = 'success'
                C-->>G: HTTP-ответ, если получен
            else Условие не выполнено, deadline_dt ещё впереди
                G->>G: Подготовить итог<br/>next_scheduled_dt = now() + scheduled_time
            else Условие не выполнено, deadline_dt наступил
                G->>C: Notify о дедлайне<br/>notification_id, notification_status = 'deadline_exceeded'
                C-->>G: HTTP-ответ, если получен
            end
        end

        opt Выполнена попытка отправки
            alt 2xx
                G->>G: Подготовить итог<br/>next_scheduled_dt = NULL, stop_dt = now()<br/>stop_reason = 'delivered' для success<br/>stop_reason = 'deadline_exceeded' для deadline_exceeded
            else 4xx, 5xx или Timeout
                G->>G: Увеличить счётчик неудачных попыток<br/>failed_attempt_count = failed_attempt_count + 1<br/>Сохранить новое значение в итоговом UPDATE
                alt Шагов повтора было меньше 13
                    G->>G: Запланировать повтор после задержки<br/>next_scheduled_dt = now() + 2^(2+N) с, N — номер шага повтора (0…12)<br/>Каждый шаг — до 3 попыток подряд
                else Лимит попыток исчерпан
                    G->>G: Подготовить итог<br/>next_scheduled_dt = NULL, stop_dt = now()<br/>stop_reason = 'delivery_failed'
                end
            end
        end

        G->>DB: UPDATE t_notification<br/>SET поля результата, lock_until_dt = NULL<br/>WHERE id = ID записи AND stop_dt IS NULL<br/>AND lock_until_dt = выданный lock_until_dt<br/>AND lock_until_dt > now()
        DB-->>G: 1 строка при успехе, 0 если резерв утрачен или запись остановлена
    end
```

</details>

<details>
<summary>Collector</summary>

```mermaid
sequenceDiagram
    participant K as Notifier Collector
    participant DB as PostgreSQL

    loop Очистка пачками
        K->>DB: SELECT id FROM t_notification<br/>WHERE stop_dt IS NOT NULL<br/>LIMIT 1000
        DB-->>K: Список ID, от 0 до 1000

        opt Выбрана хотя бы одна запись
            K->>DB: DELETE FROM t_notification<br/>WHERE id IN (выбранные ID)
            DB-->>K: Количество удалённых записей
        end

        break SELECT вернул меньше 1000 записей
            K->>K: Завершить текущий запуск
        end
    end
```

</details>

Пока остановленная нотификация хранится в базе, владелец может прочитать её через `GET`. Попытка обновить её через `PUT` возвращает `400` с заголовком ошибки `Incorrect notification status`. Для клиента `external_id` занят, пока запись хранится в базе. В этот период повторный `POST` с тем же `external_id` возвращает `409` с ID существующей нотификации. После удаления записи через API или Collector тот же `external_id` можно сразу использовать снова.

## Ограничения прототипа

| Область | Допущение прототипа |
| --- | --- |
| Входящие запросы | Вызов идёт напрямую в Notifier API с заголовком `X-Client-ID`. OAuth сервиса и API Gateway нет |
| Справочные данные | Directory за рамками прототипа, поэтому используется таблица `t_client` внутри сервиса. В ней у каждого клиента задан максимальный срок жизни нотификации (обязателен; `deadline_dt` не может быть позже `start_dt` плюс этот срок, чтобы нотификации не жили вечно), а также можно переопределить таймаут ответа клиента при доставке (по умолчанию 1 с). |
| Проверка условия | Вместо Information Services используется эмулятор. |
| Эксплуатация | За рамками прототипа остались трассировка, мониторинг, Sentry, Vault, динамическая конфигурация и эксперименты. |
| Сборка и поставка | Предусмотрена локальная сборка Docker-образа. CI/CD за рамками прототипа. |

## Локальный запуск

Нужны Docker Compose и `make`. `make up` запускает PostgreSQL, применяет миграции и ждёт готовности Notifier API и эмулятора. С хоста API доступен на `http://127.0.0.1:8080`, эмулятор — на `http://127.0.0.1:8081`. Внутри сети Compose адрес эмулятора — `http://emulator:8081`.

```bash
make up
make down      # остановить стенд, сохранив данные
```

Для работы с локальной базой используй `make psql`. `make migrate` применяет миграции и обновляет дамп схемы, а `make reset` удаляет локальную базу.

## Первая проверка уведомления

`notification.initial_check_delay_sec` в [конфигурации](notifier/configs/config.yaml) задаёт задержку первой проверки в целых секундах: от `0` до `9223372036` включительно (предел `time.Duration` при переводе секунд в наносекунды). Например, `7200` откладывает проверку на два часа. Отрицательное значение или переполнение останавливает запуск с ошибкой конфигурации. Штатное значение указано в файле; при отсутствии параметра или значении `null` загрузчик использует такое же значение по умолчанию.

При создании API сохраняет `next_scheduled_dt = now + initial_check_delay`. Эта задержка не зависит от `notification.scheduled_time_sec` (интервала повторных проверок worker) и callback backoff. Первая дата не ограничивается deadline: если deadline раньше, worker при первой обработке уже просроченной активной записи выберет `deadline_exceeded`.

Новое значение применяется после перезапуска API с обновлённой конфигурацией, только к создаваемым после этого записям. В штатном Compose конфигурация входит в образ: для её обновления нужен `make up` с пересборкой. Hot reload нет. Сохранённые даты не переносятся; API продолжает выполнять INSERT, а worker — обрабатывать старую очередь. Задержка уменьшает новую нагрузку от проверок, но не приостанавливает работу с БД.

## Тестирование

Для тестов нужен Go 1.27. `go test ./...` запускает тесты доменной логики, HTTP-обработчиков и middleware. Для расчёта покрытия используй [команду из PR-скилла](ai/skills/pr-description/SKILL.md#test-coverage).

Для интеграционных тестов PostgreSQL запусти Docker и выполни `go test -tags=integration -count=1 ./notifier/internal/storage/postgres`.

Сквозные API-тесты на pytest запускаются на текущем Docker Compose. См. [правила запуска](ai/rules/tests.mdc). Если API запущен с другой задержкой первой проверки, передай её тестам через `NOTIFIER_E2E_INITIAL_CHECK_DELAY_SEC` в секундах; эта переменная задаёт ожидание теста, конфигурацию API она не меняет.

## Структура репозитория

```
├── notifier/
│   ├── api/                           # OpenAPI Notifier: исходники в spec/, бандл в bundle/
│   ├── cmd/notifier/                  # Точка входа: режимы api и migrate
│   ├── configs/config.yaml            # Конфигурация Notifier
│   ├── db/
│   │   ├── init/                      # Бутстрап PostgreSQL
│   │   ├── migrations/                # Миграции goose
│   │   └── schema/                    # Дампы схемы, генерируются через make migrate
│   └── internal/
│       ├── application/               # Зависимости и жизненный цикл режимов
│       ├── config/                    # Загрузка конфигурации
│       ├── core/                      # Доменная логика
│       ├── server/                    # Ogen-код, HTTP-обработчики и middleware
│       ├── storage/postgres/          # Репозиторий и SQL
│       ├── interactions/              # Исходящие вызовы
│       ├── worker/                    # Опрос PostgreSQL, резервирование нотификаций и обработка пачек нотификаций
│       └── collector/                 # Очистка остановленных нотификаций
├── emulator/
│   ├── api/                           # OpenAPI эмулятора: исходники в spec/, бандл в bundle/
│   ├── cmd/emulator/                  # Точка входа эмулятора
│   └── internal/
│       ├── application/               # Зависимости и жизненный цикл эмулятора
│       ├── server/                    # Ogen-код и HTTP-обработчики
│       └── storage/memory/            # Пресеты в памяти
├── internal/                          # Общие пакеты обоих сервисов
│   ├── health/                        # Внутренние пробы и управление слушателями
│   ├── logging/                       # Контекст логирования и маскирование
│   └── problem/                       # Ответы Problem Details
├── ai/                                # Правила, скиллы и гварды для Claude Code и Codex
├── tests/e2e/                         # Сквозные API-тесты на pytest
├── build/                             # Dockerfile и общие настройки Redocly и ogen
├── scripts/                           # Скрипты генерации, схемы БД и настройки ИИ
├── docs/                              # Схемы и ADR
├── docker-compose.yaml                # PostgreSQL, миграции, API и эмулятор
├── Makefile                           # Команды разработки
├── .golangci.yaml                     # Конфигурация golangci-lint
├── go.mod                             # Общий Go-модуль
├── AGENTS.md                          # Контекст для ИИ-агентов
├── CLAUDE.md                          # Подключает AGENTS.md для Claude Code
└── README.md                          # Этот файл
```
