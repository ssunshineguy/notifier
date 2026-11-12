# Notifier

Notifier отправляет клиенту HTTP-уведомление, когда выполняется заданное клиентом условие. API сохраняет параметры, а Worker проверяет условие и доставляет результат асинхронно. В прототипе условие одно — наступление времени `time_of_onset`.

## Целевая архитектура

![Целевая схема Notifier с двумя Information Services](docs/architecture.png)

На схеме показаны два Information Service. Worker может опрашивать и дополнительные источники.

## Компоненты

| Компонент | За что отвечает |
| --- | --- |
| Balancer и RPS Limiter | Принимают входящий трафик и ограничивают частоту запросов. |
| API Gateway и Auth Service | Проверяют OAuth. Gateway передаёт в Notifier `X-Client-ID` и `X-Request-ID`. |
| Notifier API | Создаёт, читает, изменяет и останавливает нотификации. |
| PostgreSQL | Хранит параметры, расписание проверок, резервирование и результаты в `t_notification`. |
| Notifier Worker | Резервирует задачи, проверяет условия через Information Services и отправляет HTTP-уведомления клиенту. |
| Notifier Collector | Пакетно удаляет остановленные нотификации. |
| Directory | Распространяет справочные данные по сервисам платформы. В целевой схеме потребители получают их в память. |

## Жизненный цикл нотификации

1. **Создание.** Клиент передаёт параметры, включая `external_id`. API генерирует `notification_id`, сохраняет параметры в JSONB, привязывает запись к `client_id`, записывает `start_dt` и `deadline_dt`, планирует первую проверку в `next_scheduled_dt`.
2. **Управление.** Клиент читает и изменяет нотификацию по `notification_id`. Удаление останавливает дальнейшую обработку через `stop_dt` и `stop_reason = 'user_delete'`.
3. **Резервирование.** Worker выбирает готовые задачи без активного резерва через `FOR UPDATE SKIP LOCKED`. В той же транзакции он выставляет `lock_until_dt`. Размер пачки ограничен числом свободных слотов. После коммита записи обрабатываются в отдельных горутинах.
4. **Проверка и отправка.** Если условие выполнено, Worker отправляет `success`. Пока условие не выполнено и дедлайн впереди, Worker планирует следующую проверку. Если условие не выполнено к дедлайну, он отправляет `notification_stoped`. При повторе доставки Worker пропускает проверку условия и определяет статус по дедлайну.
5. **Фиксация результата.** Ответ `2xx` завершает задачу. При `4xx`, `5xx` или тайм-ауте Worker увеличивает `notification_attempts` и планирует повтор с задержкой `retry_delay × 2^(notification_attempts − 1)`. Лимит — одна исходная попытка и до `max_delivery_retries` повторов. После исчерпания лимита Worker завершает задачу с `delivery_failed`.
6. **Очистка.** Collector удаляет записи с заполненным `stop_dt` пачками до 1000. Если очередная выборка вернула меньше 1000 записей, запуск завершается.

Worker записывает результат и снимает резерв, пока запись активна, `lock_until_dt` совпадает с выданным значением и срок резерва ещё не истёк. Если задача была остановлена или резерв утрачен, обновление затрагивает ноль строк.

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
    N->>DB: INSERT INTO t_notification<br/>external_id, id = notification_id, data = params (JSONB)<br/>client_id = X-Client-ID, deadline_dt, start_dt = now()<br/>next_scheduled_dt = now() + sheduled_time
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
    N->>DB: SELECT data, deadline_dt, external_id<br/>FROM t_notification WHERE id = notification_id
    DB-->>N: data (JSONB), deadline_dt, external_id
    N-->>G: 200 OK, {data, deadline_dt, external_id}
    G-->>C: 200 OK, {data, deadline_dt, external_id}
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

    C->>G: Update notification_id<br/>OAuth, X-Request-ID<br/>Тело params с обязательным external_id
    G->>A: Проверить OAuth<br/>X-Request-ID
    A-->>G: client_id
    G->>G: Удалить OAuth и установить X-Client-ID = client_id
    G->>N: Update notification_id<br/>X-Client-ID, X-Request-ID<br/>Тело params
    N->>DB: UPDATE t_notification SET data = params (JSONB)<br/>WHERE id = notification_id
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
    N->>DB: UPDATE t_notification<br/>SET stop_dt = now(), stop_reason = 'user_delete'<br/>WHERE id = notification_id
    DB-->>N: Остановка нотификации зафиксирована
    N-->>G: 200 OK
    G-->>C: 200 OK
```

</details>

### Worker

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
        alt Повтор отправки, notification_attempts > 0
            G->>G: Если deadline_dt <= now(), notification_status = 'notification_stoped'<br/>Иначе notification_status = 'success'
            G->>C: Повторный Notify<br/>notification_id, notification_status
            C-->>G: HTTP-ответ, если получен
        else Проверка условия отправки
            G->>I1: Запросить данные о событии
            I1-->>G: Данные о событии
            G->>I2: Запросить данные о событии
            I2-->>G: Данные о событии
            G->>G: Проверить условие отправки

            alt Событие наступило
                G->>C: Notify о событии<br/>notification_id, notification_status = 'success'
                C-->>G: HTTP-ответ, если получен
            else Условие не выполнено, deadline_dt ещё впереди
                G->>G: Подготовить итог<br/>next_scheduled_dt = now() + scheduled_time
            else Условие не выполнено, deadline_dt наступил
                G->>C: Notify о дедлайне<br/>notification_id, notification_status = 'notification_stoped'
                C-->>G: HTTP-ответ, если получен
            end
        end

        opt Выполнена попытка отправки
            alt 2xx
                G->>G: Подготовить итог<br/>next_scheduled_dt = NULL, stop_dt = now()<br/>stop_reason = 'notification_seanded' для success<br/>stop_reason = 'deadline_exeeded' для notification_stoped
            else 4xx, 5xx или Timeout
                G->>G: Увеличить счётчик неудачных попыток<br/>notification_attempts = notification_attempts + 1<br/>Сохранить новое значение в итоговом UPDATE
                alt notification_attempts < 1 + max_delivery_retries
                    G->>G: Запланировать повтор после задержки<br/>next_scheduled_dt = now() + retry_delay × 2^(notification_attempts - 1)
                else Лимит попыток исчерпан
                    G->>G: Подготовить итог<br/>next_scheduled_dt = NULL, stop_dt = now()<br/>stop_reason = 'delivery_failed'
                end
            end
        end

        G->>DB: UPDATE t_notification<br/>SET поля результата, lock_until_dt = NULL<br/>WHERE id = ID записи AND stop_dt IS NULL<br/>AND lock_until_dt = выданный lock_until_dt<br/>AND lock_until_dt > now()
        DB-->>G: 1 строка при успехе, 0 если резерв утрачен или запись остановлена
    end
```

### Collector

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

## Ограничения прототипа

| Область | Допущение прототипа |
| --- | --- |
| Входящие запросы | Вызов идёт напрямую в Notifier API с заголовком `Client-ID`. OAuth и передача проверенного `X-Client-ID` относятся к целевой схеме с API Gateway. |
| Клиенты и администрирование | Клиенты хранятся в той же PostgreSQL, что и нотификации. У сервиса своя веб-админка и маршруты `/external` и `/internal`. |
| Проверка условия | Вместо Information Services используется эмулятор для `time_of_onset`. |
| Эксплуатация | За рамками прототипа остались трассировка, мониторинг, Sentry, Vault, динамическая конфигурация и эксперименты. |
| Сборка и поставка | Предусмотрена локальная сборка Docker-образа. CI/CD за рамками прототипа. |
