// SPDX-License-Identifier: MIT

// Package interop tests go-sunspec against the SunSpec Alliance's pysunspec2
// and cB-Sigma's sunspec and sunspec-sim, as packaged by
// otfabric/sunspec-interop. The tests are behind the build tag
// "interop" and need Docker; see make interop.
//
// SUNSPEC_INTEROP_OTFABRIC_IMAGE adds the adapter otfabric: a released
// go-sunspec, as sunspec-interop-otfabric. It is never a default and never
// independent evidence; the manual Self-interop workflow runs it.
package interop
