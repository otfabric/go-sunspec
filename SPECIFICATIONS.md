# Specifications

Which rule of SunSpec each behaviour of go-sunspec follows, where the rule was taken from, and the test that pins it. It exists so that a disagreement with a device or with another implementation can be settled from this repository.

**The edition and the clauses of the SunSpec specification are not recorded here.** The normative document for everything below is the SunSpec Alliance's *SunSpec Device Information Model Specification*. It could not be consulted when this file was written (2026-11-14: sunspec.org did not serve the document to the tools used), so no row cites a clause of it, and none guesses one. Each rule is mapped to the sources that could be read, listed next, and a row says "not identified" where none of them states the rule. Adding the edition and the clause to each row is open work for the maintainer; the script `utils/xml_notes_to_json_detail.py` of sunspec/models refers to version 1.2 of that specification.

## Sources

| Short name | What it is | Standing |
|---|---|---|
| schema.json | [`models/schema.json`](models/schema.json): the JSON Schema of the model definitions, from [sunspec/models](https://github.com/sunspec/models) at the commit recorded in [models/README.md](models/README.md) | Published by the SunSpec Alliance; it defines the form of a model definition (types, `sf`, `count`, `access`), not the register values |
| models | `models/model_*.json`: the 112 model definitions of the same commit | Published by the SunSpec Alliance; the layout of every model |
| pysunspec2 | The SunSpec Alliance's Python implementation, [sunspec/pysunspec2](https://github.com/sunspec/pysunspec2): `sunspec2/mb.py` and `sunspec2/device.py`, read at version 1.2.1 and at `master` on 2026-11-14, and `sunspec2/modbus/client.py` at version 1.2.1 | An implementation, not a specification: it shows what the Alliance's own code does |
| interop | The interop suite ([INTEROPERABILITY.md](INTEROPERABILITY.md)): pysunspec2 1.3.7 and cB-Sigma's sunspec and sunspec-sim, on models 1, 101, 103, 111, 120, 123, 160, 701, 702, 704, 705 | Evidence that two independent implementations agree, for what those fixtures exercise |

## Not-implemented values

`decode_point.go` (`decodePoint`), `encode_point.go` (`intSpecOf`, `encodeNotImplemented`). A point holding the value of its type is reported with `Implemented: false`; an unset point is encoded with it.

| Type | Value on the wire | Source | Test |
|---|---|---|---|
| `int16`, `sunssf` | `0x8000` | pysunspec2 `SUNS_UNIMPL_INT16`, `SUNS_UNIMPL_SUNSSF` (-32768) | `TestDecodePointAllTypes`, `TestEncodeNotImplementedDecodesAsNotImplemented`; interop |
| `uint16`, `enum16`, `bitfield16`, `count` | `0xFFFF` | pysunspec2 `SUNS_UNIMPL_UINT16`, `_ENUM16`, `_BITFIELD16`; `count` is given the `uint16` value there | the same; interop |
| `acc16`, `acc32`, `acc64` | `0` | pysunspec2 `SUNS_UNIMPL_ACC16`, `_ACC32`, `_ACC64` | `TestDecodeAccumulators`, `TestDecodePointAllTypes`; interop (64-bit accumulators) |
| `int32` | `0x80000000` | pysunspec2 `SUNS_UNIMPL_INT32` | `TestDecodePointAllTypes`, `TestHostileSignedExtremes` |
| `uint32`, `enum32`, `bitfield32` | `0xFFFFFFFF` | pysunspec2 `SUNS_UNIMPL_UINT32`, `_ENUM32`, `_BITFIELD32` | `TestDecodePointAllTypes` |
| `int64` | `0x8000000000000000` | pysunspec2 `SUNS_UNIMPL_INT64` | `TestDecodePointAllTypes`, `TestHostileSignedExtremes` |
| `uint64` | `0xFFFFFFFFFFFFFFFF` | pysunspec2 `SUNS_UNIMPL_UINT64` | `TestDecodePointAllTypes` |
| `bitfield64` | `0xFFFFFFFFFFFFFFFF` | Not identified. schema.json lists the type; no model of the registry uses it and pysunspec2, as read, does not define it. The value follows `bitfield16` and `bitfield32` | `TestDecodePointAllTypes` (the library's own tests only) |
| `float32` | Any NaN when read; `0x7FC00000` when written | pysunspec2 `SUNS_UNIMPL_FLOAT32` (`0x7fc00000`). That every NaN reads as not implemented is this library's choice: a NaN is not a measurement | `TestDecodePointFloatNaNIsUnimplemented`, `TestHostileFloats`; interop (32-bit floats) |
| `float64` | Any NaN when read; `0x7FF8000000000000` when written | pysunspec2 `SUNS_UNIMPL_FLOAT64`. No model of the registry uses the type | `TestDecodePointFloatNaNIsUnimplemented` (the library's own tests only) |
| `string` | All registers `0` | pysunspec2 `SUNS_UNIMPL_STRING` (NUL); it looks at the first character only, this library at every register, so a string that starts with NUL and continues reads as implemented here | `TestDecodePointAllTypes`, `TestHostileStrings`; interop |
| `ipaddr` | `0.0.0.0` | pysunspec2 `SUNS_UNIMPL_IPADDR` (0) | `TestDecodePointAllTypes` (the library's own tests only) |
| `ipv6addr` | The all-zero address | pysunspec2 `SUNS_UNIMPL_IPV6ADDR` (0); it looks at the first byte only | `TestDecodePointAllTypes` (the library's own tests only) |
| `eui48` | `FF:FF:FF:FF:FF:FF` in the low 48 bits | pysunspec2 `SUNS_UNIMPL_EUI48` | `TestDecodePointAllTypes` (the library's own tests only) |
| `pad` | Never "not implemented". An unset pad is written as `0x8000`; `DecodedModel.Values` leaves out a pad that holds `0x8000` | **Differs from pysunspec2**, whose `SUNS_UNIMPL_PAD` is 0 and which also treats a pad as always implemented. Which value the specification gives a pad is not identified. A pad carries no information, and a reader that follows either convention accepts both | `TestEncodeValueTable`, `TestDecodedModelValues` |
| `raw16` | Not a type this library decodes: such a point is returned as its registers, with a warning | schema.json lists the type; no model of the registry uses it | `TestDecodePointUnknownTypeReturnsRawCopy` |

## Scale factors

`decode_sf.go` (`applySF`, `scaleFactor`, `literalSF`), `encode.go` (`encScope.scaleFactor`), `write.go` (`unusableSF`).

| Rule | Source | Test |
|---|---|---|
| The `sf` of a point is a literal exponent or the name of a `sunssf` point | schema.json: `definitions.point.properties.sf` is an integer or a string. In the registry 1495 points name a scale factor point and 10 give a literal | `TestScaleFactorSources`, `TestHostileLiteralScaleFactor` |
| A scale factor is an exponent from -10 to 10. A `sunssf` point outside that range is not applied: the point stays unscaled and the model carries a warning. Encoding refuses a number outside it | schema.json limits `sf` to `minimum: -10`, `maximum: 10`, which states the range for a literal. That the same range holds for the value of a `sunssf` point is the reading of this library (`decode_sf.go`); the clause is not identified, and pysunspec2 applies no range check. Without a limit, an exponent such as 400 turns every value into an infinity or zero | `TestHostileScaleFactorOutOfRange`, `TestHostileScaleFactorOutOfRangeEncode`, `TestHostileScaleFactorOverWire`; interop covers -3 to 3 |
| A named scale factor is looked up in the point's own group instance, then in each enclosing one, outwards; one that is not implemented there does not hide a usable one further out | pysunspec2 `Point.get_value` looks in the point's group and then among the model's top-level points. For the registry both give the same result: every named scale factor is in the point's own group (733 points) or in the model's top-level group (762), none in a group in between. For a custom schema with a scale factor in an intermediate group the two differ; the clause is not identified | `TestScaleFactorScoping`, `TestScaleFactorResolution`, `TestDecodeModel705Curves`; interop (curves of model 705) |
| Reading: a point whose scale factor is not implemented, or missing, is returned unscaled (`ScaledValue` nil) | pysunspec2 `Point.get_value(computed=True)` returns the value as it is when the scale factor has no value. Clause not identified | `TestScaleFactorSources`, `TestApplySFWithoutScope` |
| Writing (`Device.WritePoint`, since v0.11.0): an engineering value is not written while the device reports the point's scale factor as not implemented or outside -10..10; the point's registers, as `[]uint16`, are | pysunspec2 `Point.set_value(computed=True)` raises `ModelError` ("SF field … value not initialized …") when the scale factor has no value, and stores the value as it is only when not asked to compute. What the specification asks of a client in this case is not identified: an open question for the maintainer | `TestWritePointScaleFactor`, `TestWritePointScaleFactorOfTheSchema` |
| Encoding (`EncodeModel`, `Server`): a point whose scale factor is not set is stored unscaled, which is how `DecodeModel` reads it back | The round trip of this library; no external source | `TestEncodeModelScaleFactors`, `TestEncodeDecodeRoundTripAllModels` |
| Rounding when encoding: the engineering value divided by 10^sf, to the nearest integer, halves away from zero | Not identified. pysunspec2 rounds with Python's `round` (halves to even) | `TestEncodeValueTable` |

## Groups and counts

`decode.go` (`group`, `instances`, `countValue`), `encode.go`.

| Rule | Source | Test |
|---|---|---|
| A group's `count` is a number or the name of a point that holds the number of instances | schema.json: `definitions.group.properties.count` is an integer or a string, default 1. In the registry 31 groups are counted by a point, all by a point of the model's top-level group (30 of type `uint16`, 1 of type `count`) | `TestDecodeModel803CountPoint`, `TestCountPointLookup`, `TestDecodeModel705Curves`; interop (model 705) |
| A count of 0 in the definition: the group repeats for as long as the model's registers last | pysunspec2 `Group._init_repeating_group` derives the number of instances from the model's length when the count is 0. Clause not identified | `TestDecodeModel160FillAndLeftover`, `TestRepeatingBlockDecode`; interop (model 160) |
| A count point that is not implemented, or negative, counts no instances and adds a warning; a count larger than the registers hold decodes the instances that are there | This library's handling of a device that does not follow its model; no external source | `TestCountPointValues`, `TestDecodeModel705CountsVersusRegisters`, `TestCountValueBounds`, `TestDecodeCountBeyondAnInt32` |
| The count point of a group is not writable by a client, whatever its `access`: it defines the layout | This library's choice; no external source | `TestWritePointRefusals`, `TestServerWrites` |

## Access

| Rule | Source | Test |
|---|---|---|
| A point is read-only unless its definition says `"access": "RW"` | schema.json: `access` is `R` or `RW`, default `R` | `TestWritePointRefusals`, `TestServerWrites`; interop (`TestPeerWritesGoServer`, `TestGoWritesPeerDevice`) |

## Modbus mapping

The marker, the base addresses that are probed, the model chain and its end marker are implemented by go-modbus (`github.com/otfabric/go-modbus/sunspec`), which this module calls for detection and discovery; they are specified and tested there. For orientation, pysunspec2 uses the same values: the marker is two registers (`SUNS_SUNS_LEN`), the end model has ID `0xFFFF` (`SUNS_END_MODEL_ID`), and its client tries the base addresses 40000, 0 and 50000. The `Server` of this module lays a device out the same way (`server.go`, `NewServer`), pinned by `TestServerLayout` and, against pysunspec2 and cB-Sigma at base addresses 0, 40000 and 50000, by the interop suite.
