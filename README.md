# Контур — контроль доступа по RFID-карте и лицу

Готовый учебный проект по ТЗ: Go-сервер, SQLite, отдельный Python-сервис распознавания и русскоязычная веб-панель. ESP32-CAM отправляет UID и JPEG; сервер проверяет карту, сравнивает лицо, записывает событие и возвращает решение.

## Что реализовано

- Журнал с автообновлением, поиском по ФИО/UID, фильтрами даты и результата, пагинацией и статистикой за московский день.
- Регистрация сотрудника по имени, карте и фотографии; блокировка, разблокировка и удаление.
- Реальное распознавание: YuNet находит лицо, SFace строит 128-мерный вектор, Go сравнивает векторы по косинусному сходству.
- Фото регистрации и прохода обрабатываются в памяти. В SQLite сохраняются векторы и журнал, изображения не сохраняются.
- Форма тестового прохода без ESP32 и консольный клиент.
- Вход администратора, сессии, проверка источника запросов, необязательный ключ устройства.
- Скрипты Windows, контейнеры Docker, автоматические проверки и Markdown-документация.

Прошивка ESP32 и монтаж RC522 относятся к аппаратной части. Проект реализует серверную часть и контракт, к которому подключается плата.

## Быстрый запуск на Windows

Нужны **Go 1.24+**, **Python 3.11** и интернет для первой установки. Проверенная конфигурация использует Python 3.11; виртуальное окружение создаётся внутри проекта.

Откройте PowerShell в папке проекта:

```powershell
# Один раз: окружение, зависимости, модели и сборка Go.
powershell -ExecutionPolicy Bypass -File .\scripts\setup.ps1

# Каждый запуск: оба сервиса; остановка — Ctrl+C.
powershell -ExecutionPolicy Bypass -File .\scripts\start.ps1
```

Откройте [http://localhost:8080](http://localhost:8080). Если `ADMIN_PASSWORD` не задан, сервер выведет случайный пароль в консоль в строке `Generated administrator password`. При следующем запуске будет создан новый пароль; данные сотрудников останутся.

Для постоянного пароля задайте его перед запуском:

```powershell
$env:ADMIN_PASSWORD = 'replace-with-your-long-password'
.\scripts\start.ps1
```

Веб-панель начинает работу с пустой базой. Добавьте сотрудника с его настоящим UID и портретом, затем проверьте проход на странице **«Проверка доступа»**. Взять случайное изображение вместо лица не получится: регистрация требует ровно одного найденного лица.

## Запуск для ESP32 в локальной сети

```powershell
$env:ADMIN_PASSWORD = 'replace-with-your-long-password'
# Необязательно. При включении тот же ключ нужно передать из прошивки.
$env:DEVICE_API_KEY = 'replace-with-your-device-key'
.\scripts\start.ps1 -Lan
```

Узнайте IPv4 компьютера командой `ipconfig`. В прошивке используйте `http://IP_КОМПЬЮТЕРА:8080/api/v1/access`, например `http://192.168.1.20:8080/api/v1/access`. Компьютер и ESP32 должны иметь сетевую связность; при запросе брандмауэра разрешите входящие подключения для частной сети. `localhost` на ESP32 обозначает саму плату.

## Список API-ручек

Основной адрес сервера — `http://localhost:8080`. В локальной сети замените `localhost` на IPv4 компьютера с сервером. Примеры ниже предназначены для **PowerShell** и используют `curl.exe`.

| Метод | Ручка | Предназначение | Что передать |
|---|---|---|---|
| GET | `/health` | Проверить работу Go-сервера и доступность SQLite | Ничего, авторизация не нужна |
| POST | `/api/v1/access` | Проверить карту и лицо, сохранить событие и разрешить/запретить проход | Multipart: `uid`, JPEG-файл `photo`; ключ устройства, если настроен |
| POST | `/api/admin/login` | Войти в панель и получить сессию администратора | JSON: `password` |
| GET | `/api/admin/session` | Проверить, действительна ли текущая сессия | Cookie `gate_session` |
| POST | `/api/admin/logout` | Выйти и завершить текущую сессию | Cookie `gate_session` |
| GET | `/api/admin/users` | Получить список зарегистрированных сотрудников | Cookie `gate_session` |
| POST | `/api/admin/users` | Зарегистрировать сотрудника и эталонный вектор его лица | Cookie; multipart: `full_name`, `card_uid`, файл `photo` |
| PATCH | `/api/admin/users/{id}` | Заблокировать или разблокировать сотрудника | Cookie; JSON: `is_active` |
| DELETE | `/api/admin/users/{id}` | Удалить сотрудника и его вектор лица, сохранив историю событий | Cookie; ID сотрудника в URL |
| GET | `/api/admin/logs` | Получить журнал с поиском, фильтрами и пагинацией | Cookie; необязательные query-параметры |
| GET | `/api/admin/stats` | Получить число сотрудников и статистику проходов за текущий московский день | Cookie `gate_session` |
| GET | `/api/admin/health` | Проверить SQLite, Python-сервис и текущие настройки проверки лица | Cookie `gate_session` |

В `{id}` подставьте числовой ID из ответа регистрации или списка сотрудников. Ниже для примера используется `1`.

## Как пользоваться API проверки доступа

```http
POST /api/v1/access
Content-Type: multipart/form-data; boundary=...
```

Ровно два multipart-поля: `uid` — **14 символов 0–9/A–F в верхнем регистре**, `photo` — файл JPEG. Использован последний вариант ТЗ: JSON содержит **только** `access` и `status`, без `action` и `message`.

```json
{"access": true, "status": "success"}
```

Отказ также приходит с HTTP 200:

```json
{"access": false, "status": "face_mismatch"}
```

Проверка через консоль:

```powershell
curl.exe -X POST http://localhost:8080/api/v1/access `
  -F 'uid=04A1B2C3D4E5F6' `
  -F 'photo=@C:\photos\capture.jpg;type=image/jpeg'
```

Если настроен `DEVICE_API_KEY`, добавьте `-H 'X-Device-Key: ВАШ_КЛЮЧ'`. Для этой ручки требуется **JPEG**; для регистрации сотрудника также допускается PNG. Лимиты изображения: до 2 МиБ, до 4 мегапикселей, не менее 32 пикселей по каждой стороне. При использовании `-F` curl сам задаёт multipart boundary; заголовок `Content-Type` вручную добавлять не нужно.

Возможные результаты:

| `status` | `access` | Значение |
|---|---|---|
| `success` | `true` | Карта активна, лицо совпало, событие сохранено |
| `unknown_uid` | `false` | Карта не зарегистрирована |
| `face_mismatch` | `false` | Лицо не совпало с эталоном владельца карты |
| `no_face_detected` | `false` | Лицо на фотографии не найдено |
| `multiple_faces` | `false` | На фотографии несколько лиц |
| `inactive_user` | `false` | Сотрудник заблокирован |
| `invalid_uid` | `false` | UID не соответствует формату |
| `invalid_request` | `false` | Неверные поля, формат, размер или содержимое файла |
| `unauthorized_device` | `false` | Ключ устройства отсутствует или неверен |
| `service_unavailable` | `false` | Сервис распознавания недоступен или вернул некорректный результат |
| `server_error` | `false` | Ошибка базы, сохранения журнала или эталонного вектора |

Обработанный запрос **POST `/api/v1/access`** возвращает HTTP 200 при разрешении и отказе. Устройство должно открывать замок только при логическом `access: true`; ошибка соединения или разбора JSON не является разрешением.

## Как пользоваться административными ручками

Выполняйте команды из папки проекта при запущенном сервере. Для всех запросов используйте один и тот же адрес: cookie, полученная для `localhost`, не подходит к `127.0.0.1` или другому IP.

### 1. Войти — POST /api/admin/login

Тело запроса — JSON с паролем администратора:

```json
{"password":"your-long-admin-password"}
```

Следующий пример запросит пароль, создаст JSON-файл и сохранит cookie для последующих команд:

```powershell
New-Item -ItemType Directory -Path .runtime -Force | Out-Null
$baseUrl = 'http://localhost:8080'
$loginJson = @{ password = (Read-Host 'Пароль администратора') } | ConvertTo-Json -Compress
[System.IO.File]::WriteAllText(
  (Join-Path (Get-Location) '.runtime/login.json'),
  $loginJson,
  [System.Text.UTF8Encoding]::new($false)
)

curl.exe -c .runtime/cookies.txt `
  -H 'Content-Type: application/json' `
  --data-binary '@.runtime/login.json' `
  "$baseUrl/api/admin/login"
```

При успехе — HTTP 200 и `{"ok":true}`. Cookie действует 8 часов; после перезапуска сервера нужно войти заново. Неверный пароль даёт 401, превышение восьми попыток за минуту — 429. JSON записывается в UTF-8 без BOM, чтобы запрос корректно читался и в Windows PowerShell 5.1. Файл пароля и cookie находятся в исключённом из Git каталоге `.runtime`.

Во всех следующих примерах `$baseUrl` задан предыдущей командой, а `-b .runtime/cookies.txt` передаёт сохранённую сессию.

### 2. Проверить сессию — GET /api/admin/session

```powershell
curl.exe -b .runtime/cookies.txt "$baseUrl/api/admin/session"
```

Действующая сессия возвращает HTTP 200 и `{"ok":true}`. При отсутствии или истечении сессии — 401; повторите вход.

### 3. Получить сотрудников — GET /api/admin/users

```powershell
curl.exe -b .runtime/cookies.txt "$baseUrl/api/admin/users"
```

Ответ — массив сотрудников с полями `id`, `full_name`, `card_uid`, `is_active`, `created_at`. Если сотрудников нет, возвращается `[]`. Фотографии и векторы лиц в ответ не включаются.

### 4. Добавить сотрудника — POST /api/admin/users

```powershell
curl.exe -b .runtime/cookies.txt `
  -F 'full_name=Иванов Иван Иванович' `
  -F 'card_uid=04A1B2C3D4E5F6' `
  -F 'photo=@C:\photos\reference.jpg' `
  "$baseUrl/api/admin/users"
```

Замените ФИО, UID и путь к портрету своими значениями. ФИО — от 2 до 120 символов, UID — уникальный, из 14 символов 0–9/A–F. На JPEG/PNG должно быть ровно одно лицо. При успехе — **HTTP 201** и объект нового активного сотрудника. Повторный UID даёт 409, отсутствие или несколько лиц — 422. Если распознавание не удалось, сотрудник не создаётся.

### 5. Заблокировать или разблокировать — PATCH /api/admin/users/{id}

Для блокировки передайте `{"is_active":false}`:

```powershell
Set-Content -LiteralPath .runtime/active.json -Value '{"is_active":false}' -Encoding ASCII
curl.exe -b .runtime/cookies.txt -X PATCH `
  -H 'Content-Type: application/json' `
  --data-binary '@.runtime/active.json' `
  "$baseUrl/api/admin/users/1"
```

Для разблокировки измените значение на `true` и повторите PATCH:

```powershell
Set-Content -LiteralPath .runtime/active.json -Value '{"is_active":true}' -Encoding ASCII
curl.exe -b .runtime/cookies.txt -X PATCH `
  -H 'Content-Type: application/json' `
  --data-binary '@.runtime/active.json' `
  "$baseUrl/api/admin/users/1"
```

Ответ — HTTP 200 и `{"ok":true}`. Если такого ID нет, сервер возвращает 404. При блокировке профиль сохраняется, но проход по карте запрещается.

### 6. Удалить сотрудника — DELETE /api/admin/users/{id}

```powershell
curl.exe -b .runtime/cookies.txt -X DELETE "$baseUrl/api/admin/users/1"
```

Ответ — HTTP 200 и `{"ok":true}`; при отсутствующем ID — 404. Удаляются профиль и вектор лица, записи журнала сохраняются. Эта команда выполняет удаление сразу; подтверждающий диалог есть только в веб-панели.

### 7. Получить журнал — GET /api/admin/logs

Без фильтров возвращаются последние 50 событий:

```powershell
curl.exe -b .runtime/cookies.txt "$baseUrl/api/admin/logs"
```

Параметры можно комбинировать:

| Параметр | Назначение | Значение по умолчанию |
|---|---|---|
| `q` | Поиск по части ФИО или UID без учёта регистра | Пусто |
| `status` | Точный статус, например `success`; `denied` — все отказы | Все статусы |
| `date` | Дата `YYYY-MM-DD` по московскому времени | Все даты |
| `limit` | Число записей, от 1 до 100 | 50 |
| `offset` | Число пропущенных записей, от 0 | 0 |

Пример: найти отказы Иванова за выбранный день:

```powershell
curl.exe -b .runtime/cookies.txt --get `
  --data-urlencode 'q=Иванов' `
  --data-urlencode 'status=denied' `
  --data-urlencode 'date=2026-10-08' `
  --data-urlencode 'limit=50' `
  --data-urlencode 'offset=0' `
  "$baseUrl/api/admin/logs"
```

Ответ — объект `{"items":[...],"total":число}`. Каждое событие содержит `id`, `timestamp`, `card_uid`, `full_name`, `result`, `duration_ms`. `total` — число событий по фильтрам до пагинации. Для следующей страницы из 50 записей задайте `offset=50`. Временные метки ответа записаны в UTC; фильтр `date` использует МСК.

### 8. Получить статистику — GET /api/admin/stats

```powershell
curl.exe -b .runtime/cookies.txt "$baseUrl/api/admin/stats"
```

Ответ содержит:

- `users` — общее число сотрудников, `active` — число активных;
- `success` и `denied` — число разрешённых и запрещённых попыток за текущий московский день;
- `date` — дата этого дня;
- `hourly` — массив из 24 счётчиков событий, от 00:00 до 23:00 МСК.

### 9. Проверить компоненты — GET /api/admin/health

```powershell
curl.exe -b .runtime/cookies.txt "$baseUrl/api/admin/health"
```

Пример ответа:

```json
{
  "database":true,
  "face_service":true,
  "device_key_required":false,
  "threshold":0.363
}
```

`database` и `face_service` показывают доступность компонентов, `device_key_required` — необходимость ключа для платы, `threshold` — порог сравнения лица. Сам ключ не возвращается.

Для простой проверки Go и SQLite без входа используйте публичную ручку:

```powershell
curl.exe http://localhost:8080/health
```

Она возвращает HTTP 200 и `{"status":"ok"}`, либо 503 и `{"status":"unavailable"}` при ошибке базы. Python эта публичная ручка не проверяет.

### 10. Выйти — POST /api/admin/logout

```powershell
curl.exe -b .runtime/cookies.txt -X POST "$baseUrl/api/admin/logout"
```

Ответ — HTTP 200 и `{"ok":true}`. После этого сохранённая сессия перестаёт действовать.

### Ошибки административного API

Административные ошибки имеют формат:

```json
{"error":"duplicate_uid","message":"Эта карта уже зарегистрирована"}
```

Основные HTTP-коды: **400** — некорректные данные, **401** — нужен вход или неверный пароль, **403** — недопустимый источник запроса, **404** — сотрудник не найден, **409** — повторный UID, **422** — фотографию невозможно использовать для регистрации, **429** — слишком частые попытки входа, **500** — ошибка сервера, **503** — распознавание недоступно. В отличие от проверки прохода, административные ручки сообщают об ошибке через HTTP-код.

## Внутренние ручки Python-сервиса

При локальном запуске адрес — `http://127.0.0.1:8001`. Эти ручки предназначены для связи Go с распознаванием; ESP32 обращается к Go на порту 8080.

| Метод | Ручка | Предназначение | Тело |
|---|---|---|---|
| GET | `/health` | Проверить готовность моделей | Не требуется |
| POST | `/encode` | Получить вектор одного лица из изображения | Сырые байты JPEG/PNG |

Проверка готовности:

```powershell
curl.exe http://127.0.0.1:8001/health
```

Ответ: `{"status":"ok","model":"sface-2021dec","dimensions":128}`.

Извлечение признаков из фотографии:

```powershell
curl.exe -X POST http://127.0.0.1:8001/encode `
  -H 'Content-Type: application/octet-stream' `
  --data-binary '@C:\photos\reference.jpg'
```

При успехе возвращаются `status: "ok"` и `encoding` — массив из 128 чисел. При ошибке — `no_face_detected`, `multiple_faces`, `invalid_image` или `service_error`. Тело здесь отправляется **без multipart**. Внутренний сервис не принимает UID и не решает, разрешать ли проход; это делает Go. В Docker его порт наружу не публикуется.

Полные схемы ответов и дополнительные подробности: **[docs/API.md](docs/API.md)**.

## Документация

| Файл | Содержание |
|---|---|
| [docs/USAGE.md](docs/USAGE.md) | Подробная установка, использование панели, настройки, сеть, резервные копии, диагностика |
| [docs/API.md](docs/API.md) | Все публичные и административные ручки, поля, статусы, HTTP-коды, примеры curl |
| [docs/CODE.md](docs/CODE.md) | Архитектура, описание файлов/функций, алгоритм распознавания, схема SQLite, потоки данных |
| [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md) | Разработка, проверки, сборка, изменение контракта и моделей |
| [docs/VERIFICATION.md](docs/VERIFICATION.md) | Выполненные проверки и их ограничения |

## Docker

```powershell
Copy-Item .env.example .env
# Откройте .env и замените ADMIN_PASSWORD своим паролем.
docker compose up --build -d
docker compose logs -f server
```

Панель доступна на порту 8080, сервис распознавания остаётся внутри сети Compose. База хранится в томе `access-data`. `docker compose down` останавливает систему и сохраняет том. Не используйте `down -v`, если данные нужно сохранить.

## Границы проекта

Это учебная система, а не готовый сертифицированный СКУД. Сравнение фотографии не проверяет живость лица и может быть обмануто фотографией человека. Для реальной проходной нужны проверка живости, настройка порога на ваших данных, HTTPS, защита и регламент хранения биометрии. Здесь сходство векторов не выдаётся за вероятность личности или «95% уверенности».

При недоступности распознавания, ошибке записи журнала или любом отрицательном результате доступ запрещается. Проект не включает всегда разрешающую заглушку в рабочем сервере.
