'use client';

import { useEffect, type RefObject } from 'react';
import { usePluginCall } from '@/api/endpoints/plugin';

// 页面只调用自身插件；宿主转发结构化消息，不解释账号、模型或登录业务。
export function usePluginPageBridge(
    iframeRef: RefObject<HTMLIFrameElement | null>,
    pluginID: string,
    pageID: string,
) {
    const { mutateAsync } = usePluginCall();
    useEffect(() => {
        const handleMessage = (event: MessageEvent) => {
            const target = iframeRef.current?.contentWindow;
            if (!target || event.source !== target) return;
            const message = event.data as Record<string, unknown> | null;
            if (!message || typeof message !== 'object' || message.pluginId !== pluginID || message.pageId !== pageID) return;
            if (message.type === 'octopus:open-url' && typeof message.url === 'string') {
                try {
                    const url = new URL(message.url);
                    if (url.protocol === 'https:' || url.protocol === 'http:') window.open(url.href, '_blank', 'noopener,noreferrer');
                } catch { /* 忽略无效地址。 */ }
                return;
            }
            if (message.type !== 'octopus:request' || typeof message.id !== 'string' || message.id.length > 100 ||
                typeof message.method !== 'string' || !/^[a-z][a-z0-9._-]{0,63}$/.test(message.method)) return;
            const reply = (payload: { ok: boolean; data?: unknown; error?: string }) => {
                if (iframeRef.current?.contentWindow !== target) return;
                target.postMessage({ type: 'octopus:reply', pluginId: pluginID, pageId: pageID, id: message.id, ...payload }, '*');
            };
            void mutateAsync({ plugin_id: pluginID, method: message.method, params: message.params ?? {} })
                .then(result => reply({ ok: true, data: result.data }))
                .catch((error: unknown) => reply({ ok: false, error: error && typeof error === 'object' && 'message' in error && typeof error.message === 'string' ? error.message : 'Plugin request failed' }));
        };
        window.addEventListener('message', handleMessage);
        return () => window.removeEventListener('message', handleMessage);
    }, [iframeRef, pluginID, pageID, mutateAsync]);
}
