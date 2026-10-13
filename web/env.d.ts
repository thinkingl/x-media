/// <reference types="vite/client" />

interface ImportMetaEnv {
  // 管理 API 鉴权密钥，需与后端配置 server.api_key 一致
  readonly VITE_API_KEY?: string
}

interface ImportMeta {
  readonly env: ImportMetaEnv
}

declare module '*.vue' {
  import type { DefineComponent } from 'vue'
  const component: DefineComponent<{}, {}, any>
  export default component
}
