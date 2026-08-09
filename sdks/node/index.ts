// openrate for Node — the sidecar client, plus a re-export of direct mode.
//
// `import { Sidecar } from "openrate"` needs no native code. Direct mode lives
// behind `openrate/direct` (and is re-exported here for convenience) and pulls
// in the optional `koffi` dependency only when you actually construct an
// Engine — importing this file does not load libopenrate.
//
// See README.md for which mode to choose. The short version: start with the
// sidecar; embed when you need an engine that provably fetches nothing.

export { Sidecar, type SidecarOptions } from "./sidecar.js";
export {
  abiVersion,
  type ConvertResult,
  type Edge,
  Engine,
  type EngineOptions,
  type Leg,
  type MetaResult,
  openHandles,
  type RateDetail,
  type RatesResult,
  Refresher,
  type RefresherOptions,
  resolveLibrary,
  type SourceStatus,
} from "./direct.js";
