'use client';

import { Bot } from 'lucide-react';
import ClaudeCode from '@lobehub/icons/es/ClaudeCode';
import Codex from '@lobehub/icons/es/Codex';
import DeepSeek from '@lobehub/icons/es/DeepSeek';
import GeminiCLI from '@lobehub/icons/es/GeminiCLI';
import CodeBuddy from '@lobehub/icons/es/CodeBuddy';
import Qoder from '@lobehub/icons/es/Qoder';
import GithubCopilot from '@lobehub/icons/es/GithubCopilot';
import { cn } from '@/lib/utils';

/**
 * Agent 品牌图标：优先使用 @lobehub/icons 的官方品牌组件（.Color 为官方配色），
 * ZCode / WorkBuddy / Kimi Code 未被任何图标库收录，使用从官方应用提取的原始图标资源。
 * Codex / Copilot 官方配色为黑白，走 Mono（currentColor）以适配明暗主题。
 */

const AGENT_LABELS: Record<string, string> = {
    'claude-code': 'Claude Code',
    codex: 'Codex',
    'deepseek-harness': 'DeepSeek Harness',
    'gemini-cli': 'Gemini CLI',
    zcode: 'ZCode',
    'kimi-code': 'Kimi Code',
    workbuddy: 'WorkBuddy',
    codebuddy: 'CodeBuddy',
    copilot: 'Copilot',
    qoder: 'Qoder',
    other: '其他 Agent',
};

export function agentDisplayName(agent: string): string {
    return AGENT_LABELS[agent] ?? '其他 Agent';
}

export function AgentIcon({ agent, className }: { agent: string; className?: string }) {
    switch (agent) {
        case 'claude-code':
            return <ClaudeCode.Color size={20} className={className} />;
        case 'codex':
            return <Codex size={20} className={className} />;
        case 'deepseek-harness':
            return <DeepSeek.Color size={20} className={className} />;
        case 'gemini-cli':
            return <GeminiCLI.Color size={20} className={className} />;
        case 'copilot':
            return <GithubCopilot size={20} className={className} />;
        case 'codebuddy':
            return <CodeBuddy.Color size={20} className={className} />;
        case 'qoder':
            return <Qoder.Color size={20} className={className} />;
        case 'zcode':
            return (
                // 20px 品牌小图标：静态导出下 next/image 无优化收益，plain img 即可
                // eslint-disable-next-line @next/next/no-img-element
                <img
                    src="/icons/zcode.png"
                    alt=""
                    width={20}
                    height={20}
                    className={cn('rounded-[4px]', className)}
                />
            );
        case 'kimi-code':
            return (
                // eslint-disable-next-line @next/next/no-img-element
                <img
                    src="/icons/kimi-code.png"
                    alt=""
                    width={20}
                    height={20}
                    className={cn('rounded-[4px]', className)}
                />
            );
        case 'workbuddy':
            return (
                // eslint-disable-next-line @next/next/no-img-element
                <img
                    src="/icons/workbuddy.svg"
                    alt=""
                    width={20}
                    height={20}
                    className={cn('rounded-[4px]', className)}
                />
            );
        default:
            return <Bot className={cn('size-5 text-muted-foreground', className)} />;
    }
}
