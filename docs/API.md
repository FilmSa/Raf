# API сервера

Базовый адрес при локальном запуске: `http://localhost:8080`. В примерах PowerShell используется **`curl.exe`**, поскольку `curl` в Windows PowerShell может быть псевдонимом другой команды. Значения и фотографии в примерах нужно заменить своими.

## 1. Общие правила

- Все JSON-ответы имеют `Content-Type: application/json; charset=utf-8`.
- Административные ручки используют cookie `gate_session`; вход выполняется паролем, отдельного логина нет.
- Биометрические векторы не возвращаются административным API.
- UID принимается строго в виде `^[0-9A-F]{14}$`. API не удаляет пробелы и не меняет регистр за клиента.
- Лимит файла: 2 МиБ = 2 097 152 байта; общий лимит multipart-запроса: ещё 64 КиБ на служебные поля.
- Изображение: не менее 32 пикселей по каждой стороне, не более 4 000 000 пикселей.
- Временные метки — UTC в формате ISO 8601, например `2026-10-07T09:30:00.000000000Z`. Фильтр даты и дневная статистика используют UTC+3.
- Кросс-доменные изменения административных данных запрещены. Браузерную панель следует открывать с того же origin, что и API.
- Неверный HTTP-метод и неизвестный URL получают стандартные 405/404. Обещание HTTP 200 относится к обработчику **POST `/api/v1/access`**.

Ошибка административной ручки:

```json
{"error":"duplicate_uid","message":"Эта карта уже зарегистрирована"}
```

## 2. Перечень ручек

| Метод | URL | Авторизация | Назначение |
|---|---|---|---|
| GET | `/health` | Нет | Проверка Go-сервера и подключения к SQLite |
| POST | `/api/v1/access` | Необязательный ключ устройства | Проверка UID и JPEG |
| POST | `/api/admin/login` | Пароль в JSON | Создание сессии |
| POST | `/api/admin/logout` | Cookie | Завершение сессии |
| GET | `/api/admin/session` | Cookie | Проверка текущей сессии |
| GET | `/api/admin/users` | Cookie | Список сотрудников |
| POST | `/api/admin/users` | Cookie | Регистрация с фотографией |
| PATCH | `/api/admin/users/{id}` | Cookie | Блокировка/разблокировка |
| DELETE | `/api/admin/users/{id}` | Cookie | Удаление сотрудника |
| GET | `/api/admin/logs` | Cookie | Журнал с фильтрами и пагинацией |
| GET | `/api/admin/stats` | Cookie | Статистика текущего московского дня |
| GET | `/api/admin/health` | Cookie | Состояние базы, распознавания и настройки |

## 3. POST /api/v1/access — контракт ESP32

Тип тела: `multipart/form-data`.

| Поле | Тип | Обязательное | Требования |
|---|---|---|---|
| `uid` | Текст | Да | Ровно 14 символов, верхний регистр |
| `photo` | Файл | Да | Бинарный JPEG; не base64, не PNG |

Должны присутствовать ровно одно текстовое поле `uid` и один файл `photo`. Повторные или посторонние поля отвергаются. Порядок частей не важен. Multipart boundary должен совпадать в заголовке и теле.

Если `DEVICE_API_KEY` задан, требуется `X-Device-Key: значение`. Встроенная тестовая форма может использовать действующую сессию администратора с того же origin; ESP32 использует ключ.

```powershell
curl.exe -X POST http://localhost:8080/api/v1/access `
  -H 'X-Device-Key: YOUR_DEVICE_KEY' `
  -F 'uid=04A1B2C3D4E5F6' `
  -F 'photo=@C:\photos\capture.jpg;type=image/jpeg'
```

Без настроенного ключа заголовок можно убрать. Не задавайте `Content-Type: multipart/form-data` вручную при использовании `-F`: curl сам добавляет правильный boundary.

Успех — HTTP 200:

```json
{"access":true,"status":"success"}
```

Отказ — также HTTP 200:

```json
{"access":false,"status":"unknown_uid"}
```

### Полный набор status

| status | access | Значение |
|---|---|---|
| `success` | true | Карта активна, лицо совпало, журнал сохранён |
| `unknown_uid` | false | Карта отсутствует или профиль удалён/заменён во время проверки |
| `face_mismatch` | false | Косинусное сходство ниже порога |
| `no_face_detected` | false | На снимке не обнаружено лицо |
| `multiple_faces` | false | Найдено больше одного лица |
| `inactive_user` | false | Сотрудник заблокирован |
| `invalid_uid` | false | UID не соответствует контракту |
| `invalid_request` | false | Ошибка multipart, отсутствует поле, файл повреждён/не JPEG/слишком большой |
| `unauthorized_device` | false | Ключ отсутствует или неверен |
| `service_unavailable` | false | Python не отвечает, вернул ошибку или некорректный вектор |
| `server_error` | false | Ошибка базы/журнала или повреждён сохранённый вектор |

Первые четыре статуса соответствуют последнему варианту ТЗ. Остальные явно описывают дополнительные причины отказа, сохраняя ту же JSON-структуру.

Для неизвестной или заблокированной карты Python не вызывается. Для известной активной карты JPEG сначала проверяется и затем передаётся в Python. Повторная проверка карты выполняется перед записью успешного решения. Если журнал не удалось записать, доступ не разрешается.

В журнале поле `result` содержит тот же строковый status. Для неизвестной карты `full_name` пустой. Для ошибочного UID `card_uid` пустой: произвольная строка не сохраняется как номер карты.

### Логика устройства

```text
отправить UID + JPEG
если HTTP не 200, ответ отсутствует или JSON повреждён:
    оставить замок закрытым
иначе если поле access имеет тип boolean и равно true:
    открыть замок / включить зелёный светодиод на 3 секунды
иначе:
    оставить замок закрытым / включить красный светодиод
```

Сервер не управляет GPIO и длительностью открытия. Это делает прошивка.

## 4. Авторизация администратора

### POST /api/admin/login

Тело `application/json`:

```json
{"password":"your-long-admin-password"}
```

```powershell
# Создайте файл .runtime/login.json с JSON пароля.
# Файл и cookie являются секретами: не добавляйте их в Git.
curl.exe -c .runtime/cookies.txt `
  -H 'Content-Type: application/json' `
  --data-binary '@.runtime/login.json' `
  http://localhost:8080/api/admin/login
```

Ответ 200: `{"ok":true}` и `Set-Cookie: gate_session=...`.

Срок сессии — 8 часов, без продления по активности. Cookie: HttpOnly, SameSite=Strict, Path=/; Secure добавляется при `COOKIE_SECURE=true`. После рестарта Go все сессии теряют силу.

Ошибки: 400 — повреждённый/избыточный JSON, 401 — неверный пароль, 403 — чужой origin, 429 — лимит попыток. Допускается восемь попыток входа за минуту с одного адреса; окно начинается с первой попытки, успешный вход сбрасывает счётчик. `X-Forwarded-For` не используется для определения адреса.

Для следующих команд сохранённая cookie передаётся через `-b .runtime/cookies.txt`.

### GET /api/admin/session

200: `{"ok":true}`, 401: `{"error":"unauthorized",...}`.

### POST /api/admin/logout

```powershell
curl.exe -b .runtime/cookies.txt -X POST http://localhost:8080/api/admin/logout
```

200: `{"ok":true}`. Сессия удаляется из памяти, браузеру отправляется удаление cookie. Без действующей сессии — 401.

## 5. Сотрудники

### GET /api/admin/users

```powershell
curl.exe -b .runtime/cookies.txt http://localhost:8080/api/admin/users
```

200, массив от новых профилей к старым:

```json
[
  {
    "id":1,
    "full_name":"Иванов Иван Иванович",
    "card_uid":"04A1B2C3D4E5F6",
    "is_active":true,
    "created_at":"2026-10-07T09:30:00.000000000Z"
  }
]
```

Пустой список — `[]`. Полный список без пагинации подходит для небольшого учебного проекта. Вектор лица не включается в ответ.

### POST /api/admin/users

Тело `multipart/form-data`:

| Поле | Тип | Требования |
|---|---|---|
| `full_name` | Текст | 2–120 Unicode-символов после TrimSpace |
| `card_uid` | Текст | 14 символов 0–9/A–F, уникальный |
| `photo` | Файл | JPEG/PNG, ровно одно лицо, общие ограничения размера |

```powershell
curl.exe -b .runtime/cookies.txt `
  -F 'full_name=Иванов Иван Иванович' `
  -F 'card_uid=04A1B2C3D4E5F6' `
  -F 'photo=@C:\photos\reference.jpg' `
  http://localhost:8080/api/admin/users
```

Успех: **201 Created**, объект сотрудника, как в списке. Новый сотрудник активен. Фото проходит распознавание до INSERT; неудачная регистрация не оставляет профиль.

Ошибки: 400 `invalid_request`/`invalid_image`, 409 `duplicate_uid`, 422 `no_face_detected`/`multiple_faces`/`invalid_image`, 503 `service_unavailable`/`service_error`, 500 `server_error`.

### PATCH /api/admin/users/{id}

Тело `application/json`; допускается только `is_active` с типом boolean:

```powershell
# Создайте .runtime/block.json с содержимым {"is_active":false}.
curl.exe -b .runtime/cookies.txt -X PATCH `
  -H 'Content-Type: application/json' `
  --data-binary '@.runtime/block.json' `
  http://localhost:8080/api/admin/users/1
```

Для разблокировки передайте `{"is_active":true}`. Успех 200: `{"ok":true}`. Ошибки: 400 — некорректный ID/JSON/отсутствующий статус, 404 — нет профиля, 500 — ошибка базы.

### DELETE /api/admin/users/{id}

```powershell
curl.exe -b .runtime/cookies.txt -X DELETE http://localhost:8080/api/admin/users/1
```

Успех 200: `{"ok":true}`. Профиль и вектор удаляются из таблицы `users`; события в `logs` остаются. Ошибки: 400 — неправильный ID, 404 — профиль уже отсутствует, 500 — ошибка базы.

API удаления не требует дополнительного токена подтверждения; подтверждающий диалог — функция интерфейса. Обладатель административной сессии может удалить профиль напрямую.

## 6. Журнал и статистика

### GET /api/admin/logs

| Query-параметр | По умолчанию | Назначение |
|---|---|---|
| `q` | Пусто | Подстрока ФИО или UID, без учёта регистра |
| `status` | Пусто | Точный result, либо `denied` для всех result, кроме `success` |
| `date` | Пусто | Московская дата `YYYY-MM-DD`; пусто — все даты |
| `limit` | 50 | 1–100 записей |
| `offset` | 0 | Неотрицательное число пропущенных записей |

```powershell
curl.exe -b .runtime/cookies.txt --get `
  --data-urlencode 'q=Иванов' `
  --data-urlencode 'status=denied' `
  --data-urlencode 'date=2026-10-07' `
  --data-urlencode 'limit=50' `
  --data-urlencode 'offset=0' `
  http://localhost:8080/api/admin/logs
```

Ответ 200:

```json
{
  "items":[
    {
      "id":7,
      "timestamp":"2026-10-07T09:31:12.000000000Z",
      "card_uid":"04A1B2C3D4E5F6",
      "full_name":"Иванов Иван Иванович",
      "result":"face_mismatch",
      "duration_ms":230
    }
  ],
  "total":1
}
```

`items` отсортированы по убыванию ID. `total` — число событий по тем же фильтрам до пагинации. `duration_ms` — время обработки до начала сохранения журнала, не полная задержка клиента. При offset за концом списка `items: []`, но `total` не обнуляется.

Ошибки 400: `invalid_date`, `invalid_limit`, `invalid_offset`; 500: `server_error`. Незнакомое текстовое значение `status` даёт пустой результат.

### GET /api/admin/stats

```json
{
  "users":12,
  "active":10,
  "success":24,
  "denied":3,
  "date":"2026-10-07",
  "hourly":[0,0,0,0,0,0,0,2,6,4,5,3,2,2,1,1,1,0,0,0,0,0,0,0]
}
```

`users`/`active` — текущие профили; `success`/`denied` — попытки за `date` по Москве. Массив `hourly` содержит 24 значения: индекс 0 — 00:00–00:59 МСК, индекс 23 — 23:00–23:59. Сумма массива равна `success + denied`. Фильтры журнала на статистику не влияют. Ошибка базы — 500.

## 7. Проверка состояния

### GET /health

Без авторизации. 200: `{"status":"ok"}`. При ошибке подключения к SQLite — 503: `{"status":"unavailable"}`. Эта ручка **не проверяет Python**.

### GET /api/admin/health

После входа. Всегда 200 при нормально работающем обработчике; состояние компонентов внутри JSON:

```json
{
  "database":true,
  "face_service":true,
  "device_key_required":false,
  "threshold":0.363
}
```

Проверка Python ограничена двумя секундами. Значение ключа устройства не возвращается.

## 8. Внутренний Python API

Это интерфейс между Go и Python, а не ручки для платы. При локальном запуске: `http://127.0.0.1:8001`. В Compose сервис не опубликован наружу. Авторизация отсутствует, поэтому его нужно оставлять приватным.

### GET /health

```json
{"status":"ok","model":"sface-2021dec","dimensions":128}
```

Модели загружаются до старта приложения: если загрузка не удалась, сервис не становится готовым.

### POST /encode

Тело — **сырые байты изображения**, не multipart. Go задаёт `Content-Type: application/octet-stream`. JPEG и PNG поддерживаются; ограничения совпадают с сервером.

```json
{"status":"ok","encoding":[0.01,-0.03,0.02]}
```

В примере массив сокращён; настоящий результат содержит 128 конечных чисел, нормированных по L2. Неудача: `{"status":"no_face_detected"}`, `multiple_faces`, `invalid_image` или `service_error`, без массива. Обычные ошибки обработки возвращаются с HTTP 200; при переполнении очереди ожидания — 503 `service_error`.

Go проверяет тип результата, размерность, отсутствие NaN/Infinity и ненулевую норму. Он сам сравнивает вектор с эталоном и принимает окончательное решение. Python не получает UID, имя или таблицу пользователей.
