# Источники протоколов

ChargeBar — самостоятельное приложение на Go/Win32. Чужие приложения и библиотеки
управления устройствами не включены. Форматы HID-пакетов и идентификаторы сверены
с опубликованными исследованиями; собственный код транспорта, UI и обработка
состояний написаны для этого проекта.

Официальные характеристики:

- [ATK Phantom](https://www.atk.store/products/atk-blazing-sky-phantom-hollow-carbon-fiber-composite-wireless-gaming-mouse): Nordic 54LM20, USB и 2,4 ГГц, GEM/Nano 8K.
- [MCHOSE V9/V9 Pro](https://support.mchose.store/hc/en-us/articles/52486723932564-V9-V9-Pro-Setup-Guide-Getting-Started-with-Your-Headsets): способы подключения, включая проводной.

Исследования протоколов:

- [noosxe/atk-tool](https://github.com/noosxe/atk-tool), MIT — классическая команда 04 и контрольная сумма.
- [papelblume/atk-tool-plus](https://github.com/papelblume/atk-tool-plus), MIT — идентификаторы Phantom и две версии протокола.
- [cobanov/atkctl](https://github.com/cobanov/atkctl), MIT — каталог мышей/приёмников, Nordic 54, отдельные команды 07 (заряд) и 0F (зарядка). Один опубликованный аппаратный ответ A9 Plus NK используется как тестовый вектор формата; это не тест на Phantom.
- [rafagfran/mchose-v9-pro-battery-tray](https://github.com/rafagfran/mchose-v9-pro-battery-tray), MIT — V9 Pro 291D:385D, 64-байтовый запрос `55 65 01`.
- [omeranha/v9pro-control](https://github.com/omeranha/v9pro-control) — описание кодов состояния V9 Pro и ограничений старых прошивок; код этого проекта не включён.

Кабельный процент V9 Pro и обмен с конкретными устройствами пользователя ещё
нужно подтвердить аппаратным тестом. Утилита не подставляет процент Windows,
расчёт по времени или случайное значение, когда ответа HID нет.
