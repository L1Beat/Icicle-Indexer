/// <reference types="vite/client" />

interface ImportMetaEnv {
  /** Base URL for the Icicle API, e.g. https://api.l1beat.io. Defaults to production. */
  readonly VITE_API_BASE_URL?: string;
}

interface ImportMeta {
  readonly env: ImportMetaEnv;
}
