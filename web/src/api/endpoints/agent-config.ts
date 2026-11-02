'use client';

import { useMutation, useQuery } from '@tanstack/react-query';
import { apiClient } from '../client';

/** agent 可编辑配置文件的读取视图 */
export interface AgentConfigFileView {
    key: string;
    path: string;
    language: string;
    sensitive: boolean;
    content: string;
    exists: boolean;
    modified_at: number;
    size: number;
}

/** GET list 响应：文件列表 + 自动配置支持信息 */
export interface AgentConfigListResponse {
    files: AgentConfigFileView[];
    /** 自动配置写入的配置文件 key，空串表示该 agent 不支持自动配置 */
    auto_config_file_key: string;
    dsh?: DshConfigState;
}

export type DshAPI = 'openai-completions' | 'openai-responses' | 'anthropic-messages';

export interface DshConfigState {
    profile: string;
    api: string;
    api_key_id: number;
    credential_configured: boolean;
    credential_reference: string;
    credential_revision: string;
    current_provider: string;
    current_model: string;
}

export interface DshPreparedConfig {
    profile: string;
    credential_reference: string;
    credential_revision: string;
    model_group_id: number;
    model_name: string;
    api: DshAPI;
    reasoning_reset: boolean;
}

export interface DshCredentialSave {
    profile: string;
    api_key_id?: number;
    model_group_id?: number;
    credential_revision?: string;
}

export interface AgentAutoConfigResponse {
    file_key: string;
    content: string;
    dsh?: DshPreparedConfig;
}

/** 单个配置文件的保存载荷 */
export interface AgentConfigSaveFile {
    key: string;
    content: string;
    base_modified_at: number;
}

/** Claude Code 各用途对应的分组 ID；未传的用途保持当前配置。 */
export interface ClaudeCodeModelGroups {
    default?: number;
    opus?: number;
    sonnet?: number;
    haiku?: number;
    fable?: number;
    subagent?: number;
}

export interface AgentAutoConfigInput {
    api_key_id: number;
    model_groups?: ClaudeCodeModelGroups;
    codex_model_group_id?: number;
    current_content?: string;
    dsh?: { profile: string; model_group_id: number; api?: DshAPI };
}

/** GET /api/v1/agent-config/list?agent=xxx */
export function useAgentConfig(agent: string | null, profile?: string) {
    return useQuery({
        queryKey: profile ? ['agent-config', agent, profile] : ['agent-config', agent],
        queryFn: async () =>
            apiClient.get<AgentConfigListResponse>(
                `/api/v1/agent-config/list?agent=${encodeURIComponent(agent ?? '')}${profile ? `&profile=${encodeURIComponent(profile)}` : ''}`,
            ),
        enabled: !!agent,
        staleTime: 0,
    });
}

/** POST /api/v1/agent-config/auto-config：生成补丁后的配置内容（不写盘，返回给编辑器草稿） */
export function useAgentAutoConfig(agent: string | null) {
    return useMutation({
        mutationFn: async (input: AgentAutoConfigInput) => {
            return apiClient.post<AgentAutoConfigResponse>(
                '/api/v1/agent-config/auto-config',
                { agent, ...input },
            );
        },
    });
}

/** POST /api/v1/agent-config/save（409 = 文件已被外部修改） */
export function useSaveAgentConfig(agent: string | null) {
    return useMutation({
        mutationFn: async (input: AgentConfigSaveFile[] | { files: AgentConfigSaveFile[]; dsh?: DshCredentialSave }) => {
            return apiClient.post('/api/v1/agent-config/save', Array.isArray(input) ? { agent, files: input } : { agent, ...input });
        },
    });
}
