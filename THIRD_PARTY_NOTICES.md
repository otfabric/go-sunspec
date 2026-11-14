# Third-party notices

go-sunspec contains no third-party source code. It carries the following
material as data, with attribution.

## SunSpec model definitions

`models/model_*.json` and `models/schema.json` are the SunSpec information
model definitions and their JSON Schema, copied unmodified from
[sunspec/models](https://github.com/sunspec/models) (`json/`), the repository
of the SunSpec Alliance. The commit they were taken from, the date and the
SHA-256 of every file are in [models/README.md](models/README.md) and
[models/SHA256SUMS](models/SHA256SUMS).

`registry/models_gen.go` is generated from those files by `internal/gen`
(`make generate`): the names, labels, descriptions, types, units, scale
factors and symbols of every model, group and point. It is compiled into
every program that imports go-sunspec.

The model definitions are licensed under the Apache License, Version 2.0. The
licence text, as upstream distributes it, is in
[models/LICENSE](models/LICENSE); it is also at
<http://www.apache.org/licenses/LICENSE-2.0>. Upstream has no `NOTICE` file,
and the files carry no copyright notice of their own.

Everything else in this repository is under the MIT License: [LICENSE](LICENSE).
