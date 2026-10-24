# Upstream

This package is a Go port of [cschneegans/unattend-generator](https://github.com/cschneegans/unattend-generator)
(MIT, © 2024-2025 Christoph Schneegans, see `LICENSE-unattend-generator.txt`).

- Ported commit: `27237f82e9a8498d9c5bab7d398dc34b7682a1e8` (11.10.2026).
- `resource/` is copied from that commit without changes.
- The port is checked against the original: `testdata/golden/*.json` are settings,
  `*.xml` (or `*.err`) what the original generator made of them, regenerated with
  `scripts/unattend-golden.sh` (needs the .NET 8 SDK).

Not ported: the final XSD validation of the generated document
(`Util.ValidateAgainstSchema`). The golden files cover the output, and the
editor does not let invalid values through.

## Порядок компонентов

В оригинале `Components` — `ImmutableDictionary`, и порядок, в котором
компоненты из раздела «Свой XML» дописываются в проход, на сайте
случаен. Порт добавляет их в порядке настроек. Поэтому эталоны держат не
больше одного своего компонента на проход — иначе сравнение байт в байт
зависело бы от хешей .NET.

## Что ещё не перенесено

Разметка диска и PE-скрипты (кроме варианта по умолчанию), удаление
программ, оптимизации и профили Wi-Fi пока возвращают ошибку
`Not supported yet: …` — генерация не падает, а сообщает, что этих
настроек порт ещё не умеет. Они переносятся задачами 2–5 плана.
