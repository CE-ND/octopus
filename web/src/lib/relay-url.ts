import { API_BASE_URL } from '@/api/client';

// 解析对客户端暴露的完整请求地址（OpenAI 兼容端点前缀）：
// 显式配置了后端地址（开发模式）时用配置值，否则前端由后端托管、与页面同源
// 组件经 ContentLoader 客户端动态加载，初始化时 window 必然可用；SSG 预渲染兜底返回空串
export function resolveRelayBaseUrl(): string {
    if (API_BASE_URL && API_BASE_URL !== '.') {
        return `${API_BASE_URL.replace(/\/+$/, '')}/v1`;
    }
    if (typeof window === 'undefined') return '';
    return `${window.location.origin}/v1`;
}
