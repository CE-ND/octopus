'use client';

import { useQuery } from '@tanstack/react-query';
import { apiClient } from '@/api/client';

/** 网关 → 分组 的一条调用链路 */
export interface TopologyGroupLink {
    group_id: number;
    group_name: string;
    model: string;
    protocol: string;
    active: number;
    calls: number;
    last_seen: number;
    last_success?: boolean | null;
}

/** 一个调用方（agent + 密钥）的聚合视图 */
export interface TopologyCaller {
    agent: string;
    api_key_name: string;
    active: number;
    calls: number;
    last_seen: number;
    groups: TopologyGroupLink[];
}

/** 本机安装发现结果 */
export interface TopologyInstalledAgent {
    agent: string;
    name: string;
    installed: boolean;
    has_config: boolean;
    auto_config: boolean;
}

export interface TopologyLiveView {
    now: number;
    installed: TopologyInstalledAgent[];
    callers: TopologyCaller[];
}

/** 活跃判定：有在途请求，或 60 秒内有过调用（与后端 TopologyActiveWindow 一致） */
export function isTopologyNodeActive(active: number, lastSeen: number, now: number): boolean {
    return active > 0 || now - lastSeen < 60;
}

/**
 * 实时调用拓扑：本机哪些 agent 正在调用网关、走了哪个分组。
 * 有活跃调用时 2s 快轮询，空闲时 8s 慢轮询（仿 group-health 自适应模式）。
 */
export function useTopologyLive() {
    return useQuery({
        queryKey: ['topology', 'live'],
        queryFn: async () => {
            return apiClient.get<TopologyLiveView>('/api/v1/topology/live');
        },
        refetchInterval: (query) => {
            const data = query.state.data as TopologyLiveView | undefined;
            const hasActive = data?.callers.some((caller) => caller.active > 0);
            return hasActive ? 2000 : 8000;
        },
    });
}
