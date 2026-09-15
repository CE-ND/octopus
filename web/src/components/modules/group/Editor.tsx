'use client';

import { useCallback, useMemo, useState, type FormEvent } from 'react';
import { Check, ChevronDown, Plus, Search, Sparkles, Trash2 } from 'lucide-react';
import { useTranslations } from 'next-intl';
import { AnimatePresence, motion } from 'motion/react';
import { useModelChannelList, type LLMChannel } from '@/api/endpoints/model';
import { Button } from '@/components/ui/button';
import { Field, FieldGroup, FieldLabel } from '@/components/ui/field';
import { Input } from '@/components/ui/input';
import { Switch } from '@/components/ui/switch';
import { cn } from '@/lib/utils';
import { getModelIcon } from '@/lib/model-icons';
import { CopyIconButton } from '@/components/common/CopyButton';
import type { GroupMode } from '@/api/endpoints/group';
import type { SelectedMember } from './ItemList';
import { MemberList } from './ItemList';
import { matchesGroupName, memberKey, normalizeKey, MODE_LABELS } from './utils';
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '@/components/animate-ui/components/animate/tooltip';
import { HelpCircle } from 'lucide-react';



export type GroupEditorValues = {
    name: string;
    match_regex: string;
    mode: GroupMode;
    first_token_time_out: number;
    session_keep_time: number;
    retry_enabled: boolean;
    max_retries: number;
    members: SelectedMember[];
};

const MANUAL_SITE_KEY = '__manual__';

type PickerChannelNode = {
    key: string;
    label: string; // 分组名-协议（default-Anthropic），手动渠道用 channel_name
    models: LLMChannel[];
};

type PickerAccountNode = {
    key: string;
    label: string;
    channels: PickerChannelNode[];
    modelCount: number;
};

type PickerSiteNode = {
    key: string;
    label: string;
    accounts: PickerAccountNode[];
    modelCount: number;
};

/** 按 站点 → 账号 → 协议渠道 三级分桶；无站点归属的手动渠道归入兜底桶。 */
function buildSiteTree(modelChannels: LLMChannel[]): PickerSiteNode[] {
    const siteBuckets = new Map<string, PickerSiteNode>();
    const manualModels: LLMChannel[] = [];

    const siteKeyOf = (mc: LLMChannel) =>
        mc.site_id != null ? `site:${mc.site_id}` : `site:${(mc.site_name || '').trim() || 'unknown'}`;
    // 账号按「站点内名称」合并：同一站点下常存在多个同名账号（重复导入/同步
    // 产生，id 不同但实际是同一用户），展示层面合并为一个节点，渠道仍按
    // channel_id 区分。
    const accountKeyOf = (mc: LLMChannel) => {
        const name = (mc.site_account_name || '').trim();
        return name ? `acc:${name}` : 'acc:default';
    };
    const channelKeyOf = (mc: LLMChannel) => `ch:${mc.channel_id}`;

    for (const mc of modelChannels) {
        if (mc.site_id == null && !(mc.site_name || '').trim()) {
            manualModels.push(mc);
            continue;
        }
        const sKey = siteKeyOf(mc);
        let site = siteBuckets.get(sKey);
        if (!site) {
            site = { key: sKey, label: (mc.site_name || '').trim() || '未知站点', accounts: [], modelCount: 0 };
            siteBuckets.set(sKey, site);
        }
        const aKey = accountKeyOf(mc);
        // find 的键必须与创建时的复合键一致，否则每行都会新建节点。
        let account = site.accounts.find((a) => a.key === `${sKey}/${aKey}`);
        if (!account) {
            account = { key: `${sKey}/${aKey}`, label: (mc.site_account_name || '').trim() || '默认账号', channels: [], modelCount: 0 };
            site.accounts.push(account);
        }
        const cKey = channelKeyOf(mc);
        let channel = account.channels.find((c) => c.key === `${aKey}/${cKey}`);
        if (!channel) {
            channel = {
                key: `${aKey}/${cKey}`,
                label: [mc.site_group_name, mc.endpoint_type].map((v) => v?.trim?.() ?? '').filter(Boolean).join('-') || mc.channel_name,
                models: [],
            };
            account.channels.push(channel);
        }
        channel.models.push(mc);
    }

    const sortByLabel = <T extends { label: string }>(items: T[]) =>
        [...items].sort((a, b) => a.label.localeCompare(b.label));

    const sites = Array.from(siteBuckets.values()).map((site) => ({
        ...site,
        accounts: sortByLabel(site.accounts).map((account) => ({
            ...account,
            channels: sortByLabel(account.channels).map((channel) => ({
                ...channel,
                models: [...channel.models].sort((a, b) => a.name.localeCompare(b.name)),
            })),
            modelCount: account.channels.reduce((acc, c) => acc + c.models.length, 0),
        })),
        modelCount: 0,
    }));
    for (const site of sites) {
        site.modelCount = site.accounts.reduce((acc, a) => acc + a.modelCount, 0);
    }

    if (manualModels.length > 0) {
        const byChannel = new Map<number, PickerChannelNode>();
        for (const mc of manualModels) {
            const cKey = channelKeyOf(mc);
            let channel = byChannel.get(mc.channel_id);
            if (!channel) {
                channel = { key: `${MANUAL_SITE_KEY}/${cKey}`, label: mc.channel_name, models: [] };
                byChannel.set(mc.channel_id, channel);
            }
            channel.models.push(mc);
        }
        const channels = sortByLabel(Array.from(byChannel.values())).map((channel) => ({
            ...channel,
            models: [...channel.models].sort((a, b) => a.name.localeCompare(b.name)),
        }));
        sites.push({
            key: MANUAL_SITE_KEY,
            label: '手动渠道',
            accounts: [{
                key: `${MANUAL_SITE_KEY}/acc`,
                label: '手动渠道',
                channels,
                modelCount: manualModels.length,
            }],
            modelCount: manualModels.length,
        });
    }
    return sites;
}

/** 站点名/账号名/渠道名/模型名任一命中即保留对应层级。 */
function filterSiteTree(sites: PickerSiteNode[], normalizedSearch: string): PickerSiteNode[] {
    if (!normalizedSearch) return sites;
    const result: PickerSiteNode[] = [];
    for (const site of sites) {
        const siteMatch = site.label.toLowerCase().includes(normalizedSearch);
        const accounts: PickerAccountNode[] = [];
        for (const account of site.accounts) {
            const accountMatch = account.label.toLowerCase().includes(normalizedSearch);
            const channels: PickerChannelNode[] = [];
            for (const channel of account.channels) {
                const channelMatch = channel.label.toLowerCase().includes(normalizedSearch);
                const models = channel.models.filter((m) => m.name.toLowerCase().includes(normalizedSearch));
                if (siteMatch || accountMatch || channelMatch) {
                    channels.push(channel);
                } else if (models.length > 0) {
                    channels.push({ ...channel, models });
                }
            }
            if (siteMatch || accountMatch || channels.length > 0) {
                accounts.push({ ...account, channels });
            }
        }
        if (siteMatch || accounts.length > 0) {
            result.push({ ...site, accounts });
        }
    }
    return result;
}

function ModelPickerSection({
    modelChannels,
    selectedMembers,
    onAdd,
    onAutoAdd,
    autoAddDisabled,
}: {
    modelChannels: LLMChannel[];
    selectedMembers: SelectedMember[];
    onAdd: (channel: LLMChannel) => void;
    onAutoAdd: () => void;
    autoAddDisabled: boolean;
}) {
    const t = useTranslations('group');
    const [searchKeyword, setSearchKeyword] = useState('');
    const [expanded, setExpanded] = useState<Set<string>>(new Set());

    const selectedKeys = useMemo(() => new Set(selectedMembers.map(memberKey)), [selectedMembers]);
    const normalizedSearch = searchKeyword.trim().toLowerCase();

    const toggleExpanded = useCallback((key: string) => {
        setExpanded((current) => {
            const next = new Set(current);
            if (next.has(key)) next.delete(key);
            else next.add(key);
            return next;
        });
    }, []);

    const sites = useMemo(() => buildSiteTree(modelChannels), [modelChannels]);
    const filteredSites = useMemo(
        () => filterSiteTree(sites, normalizedSearch),
        [sites, normalizedSearch]
    );
    const forceExpand = normalizedSearch.length > 0;

    return (
        <div className="rounded-xl border border-border/50 bg-muted/30 flex flex-col min-h-0">
            <div className="grid grid-cols-[1fr_auto_1fr] items-center gap-2 px-3 py-2 border-b border-border/30 bg-muted/50">
                <span className="min-w-0 justify-self-start text-sm font-medium text-foreground">
                    {t('form.addItem')}
                </span>

                <div className="relative justify-self-center w-30">
                    <Search className="pointer-events-none absolute left-2 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" />
                    <Input
                        value={searchKeyword}
                        onChange={(event) => setSearchKeyword(event.target.value)}
                        className="h-6 rounded-lg border-border/60 bg-background/70 pl-7 pr-2 text-xs shadow-none focus-visible:border-border/60 focus-visible:ring-0"
                        aria-label="search"
                    />
                </div>

                <button
                    type="button"
                    onClick={onAutoAdd}
                    className={cn(
                        'justify-self-end shrink-0 flex items-center gap-1 px-2 py-1 rounded-lg text-xs font-medium transition-colors',
                        autoAddDisabled
                            ? 'text-muted-foreground/50 cursor-not-allowed'
                            : 'hover:bg-muted text-muted-foreground hover:text-foreground'
                    )}
                    disabled={autoAddDisabled}
                    title={t('form.autoAdd')}
                >
                    <Sparkles className="size-3.5" />
                    <span>{t('form.autoAdd')}</span>
                </button>
            </div>

            <div className="flex-1 min-h-0 overflow-y-auto p-2">
                {filteredSites.length === 0 ? (
                    <div className="flex h-full items-center justify-center text-sm text-muted-foreground">
                        暂无可添加的模型
                    </div>
                ) : (
                    <div className="w-full space-y-1.5">
                        {filteredSites.map((site) => {
                            const siteModels = nodeModels(site);
                            const siteTotal = siteModels.length;
                            const siteSelected = countSelected(siteModels, selectedKeys);
                            const siteAvailable = siteTotal - siteSelected;
                            const siteExpanded = forceExpand || expanded.has(site.key);

                            return (
                                <div key={site.key}>
                                    <TreeNodeButton
                                        expanded={siteExpanded}
                                        onClick={() => toggleExpanded(site.key)}
                                        label={site.label}
                                        count={{ available: siteAvailable, total: siteTotal }}
                                        variant="site"
                                    />

                                    <AnimatePresence initial={false}>
                                        {siteExpanded ? (
                                            <motion.div
                                                key="site-content"
                                                initial={{ height: 0, opacity: 0 }}
                                                animate={{ height: 'auto', opacity: 1 }}
                                                exit={{ height: 0, opacity: 0 }}
                                                transition={{ duration: 0.2, ease: 'easeOut' }}
                                                className="overflow-hidden"
                                            >
                                                <div className="mt-1 space-y-1 pl-4">
                                                    {site.accounts.map((account) => {
                                                        const accModels = nodeModels(account);
                                                        const accSelectedCount = countSelected(accModels, selectedKeys);
                                                        const accAvailable = accModels.length - accSelectedCount;
                                                        const accExpanded = forceExpand || expanded.has(account.key);

                                                        return (
                                                            <div key={account.key}>
                                                                <TreeNodeButton
                                                                    expanded={accExpanded}
                                                                    onClick={() => toggleExpanded(account.key)}
                                                                    label={account.label}
                                                                    count={{ available: accAvailable, total: account.modelCount }}
                                                                    variant="account"
                                                                />

                                                                <AnimatePresence initial={false}>
                                                                    {accExpanded ? (
                                                                        <motion.div
                                                                            key="account-content"
                                                                            initial={{ height: 0, opacity: 0 }}
                                                                            animate={{ height: 'auto', opacity: 1 }}
                                                                            exit={{ height: 0, opacity: 0 }}
                                                                            transition={{ duration: 0.2, ease: 'easeOut' }}
                                                                            className="overflow-hidden"
                                                                        >
                                                                            <div className="mt-1 space-y-1 pl-4">
                                                                                {account.channels.map((channel) => {
                                                                                    const chSelected = channel.models.filter((m) => selectedKeys.has(memberKey(m))).length;
                                                                                    const chAvailable = channel.models.length - chSelected;
                                                                                    const chExpanded = forceExpand || expanded.has(channel.key);

                                                                                    return (
                                                                                        <div key={channel.key}>
                                                                                            <TreeNodeButton
                                                                                                expanded={chExpanded}
                                                                                                onClick={() => toggleExpanded(channel.key)}
                                                                                                label={channel.label}
                                                                                                count={{ available: chAvailable, total: channel.models.length }}
                                                                                                variant="channel"
                                                                                            />

                                                                                            <AnimatePresence initial={false}>
                                                                                                {chExpanded ? (
                                                                                                    <motion.div
                                                                                                        key="channel-content"
                                                                                                        initial={{ height: 0, opacity: 0 }}
                                                                                                        animate={{ height: 'auto', opacity: 1 }}
                                                                                                        exit={{ height: 0, opacity: 0 }}
                                                                                                        transition={{ duration: 0.2, ease: 'easeOut' }}
                                                                                                        className="overflow-hidden"
                                                                                                    >
                                                                                                        <div className="mt-1 space-y-1 pl-4">
                                                                                                            {channel.models.map((m) => (
                                                                                                                <ModelRow
                                                                                                                    key={memberKey(m)}
                                                                                                                    model={m}
                                                                                                                    isSelected={selectedKeys.has(memberKey(m))}
                                                                                                                    onAdd={onAdd}
                                                                                                                    copyTooltip={t('detail.actions.copyName')}
                                                                                                                    addLabel={t('form.addItem')}
                                                                                                                />
                                                                                                            ))}
                                                                                                        </div>
                                                                                                    </motion.div>
                                                                                                ) : null}
                                                                                            </AnimatePresence>
                                                                                        </div>
                                                                                    );
                                                                                })}
                                                                            </div>
                                                                        </motion.div>
                                                                    ) : null}
                                                                </AnimatePresence>
                                                            </div>
                                                        );
                                                    })}
                                                </div>
                                            </motion.div>
                                        ) : null}
                                    </AnimatePresence>
                                </div>
                            );
                        })}
                    </div>
                )}
            </div>
        </div>
    );
}

/** 节点下的全部模型（含子层展开），供计数与选中统计直接遍历。 */
function nodeModels(node: PickerSiteNode | PickerAccountNode | PickerChannelNode): LLMChannel[] {
    if ('models' in node) return node.models;
    if ('accounts' in node) return node.accounts.flatMap((a) => nodeModels(a));
    if ('channels' in node) return node.channels.flatMap((c) => c.models);
    return [];
}

function countSelected(models: LLMChannel[], selectedKeys: Set<string>): number {
    return models.reduce((acc, m) => acc + (selectedKeys.has(memberKey(m)) ? 1 : 0), 0);
}

function TreeNodeButton({
    expanded,
    onClick,
    label,
    count,
    variant,
}: {
    expanded: boolean;
    onClick: () => void;
    label: string;
    count: { available: number; total: number };
    variant: 'site' | 'account' | 'channel';
}) {
    const styles = {
        site: {
            row: 'w-full flex items-center gap-2 rounded-lg bg-muted px-2 py-2 text-left transition-colors hover:bg-muted/80',
            text: 'min-w-0 flex-1 truncate text-sm font-semibold text-foreground',
            count: 'shrink-0 text-xs tabular-nums text-muted-foreground',
            chevron: 'size-3.5',
        },
        account: {
            row: 'w-full flex items-center gap-2 rounded-md px-2 py-1.5 text-left transition-colors hover:bg-muted/60',
            text: 'min-w-0 flex-1 truncate text-xs font-medium text-foreground/90',
            count: 'shrink-0 text-[10px] tabular-nums text-muted-foreground',
            chevron: 'size-3',
        },
        channel: {
            row: 'w-full flex items-center gap-2 rounded-md px-2 py-1 text-left transition-colors hover:bg-muted/40',
            text: 'min-w-0 flex-1 truncate text-xs text-muted-foreground',
            count: 'shrink-0 text-[10px] tabular-nums text-muted-foreground/70',
            chevron: 'size-3',
        },
    }[variant];

    return (
        <button type="button" onClick={onClick} className={styles.row}>
            <ChevronDown
                className={cn(
                    styles.chevron,
                    'shrink-0 text-muted-foreground transition-transform',
                    expanded ? '' : '-rotate-90'
                )}
            />
            <span className={styles.text}>{label}</span>
            <span className={styles.count}>
                {count.available}/{count.total}
            </span>
        </button>
    );
}

function ModelRow({
    model,
    isSelected,
    onAdd,
    copyTooltip,
    addLabel,
}: {
    model: LLMChannel;
    isSelected: boolean;
    onAdd: (channel: LLMChannel) => void;
    copyTooltip: string;
    addLabel: string;
}) {
    const { Avatar } = getModelIcon(model.name);
    // 与右侧已选列表的 sourceLabel 一致：站点渠道显示投影出的
    // 站点/账号/分组-协议 完整路径，手动渠道补充协议后缀。
    const isSiteChannel = model.site_id != null;
    const sourceLabel = [model.channel_name, isSiteChannel ? null : model.endpoint_type?.trim()]
        .filter(Boolean)
        .join(' · ');
    return (
        <div
            className={cn(
                'w-full flex items-center gap-1 rounded-lg border border-border/50 bg-background px-1.5 py-1.5 transition-colors',
                !isSelected && 'hover:bg-muted'
            )}
        >
            <button
                type="button"
                onClick={() => !isSelected && onAdd(model)}
                disabled={isSelected}
                className={cn(
                    'flex min-w-0 flex-1 items-center gap-2 rounded-md px-1 py-0.5 text-left outline-none focus-visible:ring-2 focus-visible:ring-ring',
                    isSelected ? 'cursor-not-allowed opacity-60' : 'cursor-pointer'
                )}
            >
                <Avatar size={16} />
                <span className="min-w-0 flex flex-col">
                    <span className="text-sm font-medium truncate leading-tight">{model.name}</span>
                    {sourceLabel && (
                        <span className="text-[10px] text-muted-foreground truncate leading-tight">{sourceLabel}</span>
                    )}
                </span>
            </button>

            <Tooltip side="top" sideOffset={8} align="center">
                <TooltipTrigger>
                    <CopyIconButton
                        text={model.name}
                        className="shrink-0 rounded-md p-1 text-muted-foreground transition-colors hover:bg-background hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                        copyIconClassName="size-3.5"
                        checkIconClassName="size-3.5 text-primary"
                    />
                </TooltipTrigger>
                <TooltipContent>{copyTooltip}</TooltipContent>
            </Tooltip>

            <button
                type="button"
                onClick={() => !isSelected && onAdd(model)}
                disabled={isSelected}
                aria-label={isSelected ? model.name : `${addLabel}: ${model.name}`}
                className={cn(
                    'shrink-0 rounded-md p-1 text-muted-foreground outline-none transition-colors focus-visible:ring-2 focus-visible:ring-ring',
                    isSelected ? 'cursor-not-allowed opacity-60' : 'hover:bg-background hover:text-foreground'
                )}
            >
                {isSelected ? (
                    <Check className="size-4 text-primary" />
                ) : (
                    <Plus className="size-4" />
                )}
            </button>
        </div>
    );
}

function SortSection({
    members,
    onReorder,
    onRemove,
    onWeightChange,
    removingIds,
    showWeight,
    onClear,
}: {
    members: SelectedMember[];
    onReorder: (members: SelectedMember[]) => void;
    onRemove: (id: string) => void;
    onWeightChange: (id: string, weight: number) => void;
    removingIds: Set<string>;
    showWeight: boolean;
    onClear: () => void;
}) {
    const t = useTranslations('group');

    return (
        <div className="rounded-xl border border-border/50 bg-muted/30 flex flex-col min-h-0">
            <div className="flex items-center justify-between px-3 py-2 border-b border-border/30 bg-muted/50">
                <span className="text-sm font-medium text-foreground">
                    {t('form.items')}
                    {members.length > 0 && (
                        <span className="ml-1.5 text-xs text-muted-foreground font-normal">
                            ({members.length})
                        </span>
                    )}
                </span>
                <button
                    type="button"
                    onClick={onClear}
                    disabled={members.length === 0}
                    className={cn(
                        'flex items-center gap-1 px-2 py-1 rounded-lg text-xs font-medium transition-colors',
                        members.length === 0
                            ? 'text-muted-foreground/50 cursor-not-allowed'
                            : 'hover:bg-muted text-muted-foreground hover:text-foreground'
                    )}
                    title={t('form.clear')}
                >
                    <Trash2 className="size-3.5" />
                    <span>{t('form.clear')}</span>
                </button>
            </div>

            <div className="flex-1 min-h-0">
                <MemberList
                    members={members}
                    onReorder={onReorder}
                    onRemove={onRemove}
                    onWeightChange={onWeightChange}
                    removingIds={removingIds}
                    showWeight={showWeight}
                    showConfirmDelete={false}
                />
            </div>
        </div>
    );
}

export function GroupEditor({
    initial,
    submitText,
    submittingText,
    isSubmitting,
    onSubmit,
    onCancel,
    nameLabel,
}: {
    initial?: Partial<GroupEditorValues>;
    submitText: string;
    submittingText: string;
    isSubmitting: boolean;
    onSubmit: (values: GroupEditorValues) => void;
    onCancel?: () => void;
    nameLabel?: string;
}) {
    const t = useTranslations('group');
    const { data: modelChannels = [] } = useModelChannelList();

    const [groupName, setGroupName] = useState(initial?.name ?? '');
    const [matchRegex, setMatchRegex] = useState(initial?.match_regex ?? '');
    const [mode, setMode] = useState<GroupMode>((initial?.mode ?? 1) as GroupMode);
    const [firstTokenTimeOut, setFirstTokenTimeOut] = useState<number>(initial?.first_token_time_out ?? 0);
    const [sessionKeepTime, setSessionKeepTime] = useState<number>(initial?.session_keep_time ?? 0);
    const [retryEnabled, setRetryEnabled] = useState<boolean>(initial?.retry_enabled ?? false);
    const [maxRetries, setMaxRetries] = useState<number>(initial?.max_retries ?? 3);
    const [selectedMembers, setSelectedMembers] = useState<SelectedMember[]>(initial?.members ?? []);
    const [removingIds, setRemovingIds] = useState<Set<string>>(new Set());

    const groupKey = normalizeKey(groupName);
    const regexKey = matchRegex.trim();

    const { matchedModelChannels, regexError } = useMemo(() => {
        const parseRegex = (input: string): RegExp => {
            const inlineMatch = input.match(/^\(\?([ism]+)\)(.+)$/);
            if (inlineMatch) {
                const flagMap: Record<string, string> = { i: 'i', s: 's', m: 'm' };
                const flags = inlineMatch[1].split('').map(f => flagMap[f] || '').join('');
                return new RegExp(inlineMatch[2], flags);
            }

            return new RegExp(input);
        };

        if (regexKey) {
            try {
                const re = parseRegex(regexKey);
                return { matchedModelChannels: modelChannels.filter((mc) => re.test(mc.name)), regexError: '' };
            } catch (e) {
                return { matchedModelChannels: [], regexError: (e as Error)?.message ?? 'Invalid regex' };
            }
        }
        if (!groupKey) return { matchedModelChannels: [], regexError: '' };
        return { matchedModelChannels: modelChannels.filter((mc) => matchesGroupName(mc.name, groupKey)), regexError: '' };
    }, [groupKey, regexKey, modelChannels]);

    const handleAddMember = useCallback((channel: LLMChannel) => {
        const key = memberKey(channel);
        setSelectedMembers((prev) => {
            if (prev.some((m) => m.id === key)) return prev;
            return [...prev, { ...channel, id: key, weight: 1 }];
        });
    }, []);

    const autoAddDisabled = useMemo(() => {
        if ((!regexKey && !groupKey) || regexError || matchedModelChannels.length === 0) return true;
        const existing = new Set(selectedMembers.map((m) => m.id));
        return matchedModelChannels.every((mc) => existing.has(memberKey(mc)));
    }, [groupKey, regexKey, regexError, matchedModelChannels, selectedMembers]);

    const handleAutoAdd = useCallback(() => {
        if (matchedModelChannels.length === 0) return;
        setSelectedMembers((prev) => {
            const existing = new Set(prev.map((m) => m.id));
            const toAdd = matchedModelChannels
                .filter((mc) => !existing.has(memberKey(mc)))
                .map((mc) => ({ ...mc, id: memberKey(mc), weight: 1 }));
            return toAdd.length ? [...prev, ...toAdd] : prev;
        });
    }, [matchedModelChannels]);

    const handleWeightChange = useCallback((id: string, weight: number) => {
        setSelectedMembers((prev) => prev.map((m) => m.id === id ? { ...m, weight } : m));
    }, []);

    const handleRemoveMember = useCallback((id: string) => {
        setRemovingIds((prev) => new Set(prev).add(id));
        setTimeout(() => {
            setSelectedMembers((prev) => prev.filter((m) => m.id !== id));
            setRemovingIds((prev) => { const n = new Set(prev); n.delete(id); return n; });
        }, 200);
    }, []);

    const handleClearMembers = useCallback(() => {
        setSelectedMembers([]);
        setRemovingIds(new Set());
    }, []);

    const isValid = groupKey.length > 0 && selectedMembers.length > 0 && !regexError;

    const handleSubmit = (event: FormEvent<HTMLFormElement>) => {
        event.preventDefault();
        if (!isValid) return;
        onSubmit({
            name: groupName,
            match_regex: regexKey,
            mode,
            first_token_time_out: firstTokenTimeOut,
            session_keep_time: sessionKeepTime,
            retry_enabled: retryEnabled,
            max_retries: maxRetries,
            members: selectedMembers,
        });
    };


    return (
        <form onSubmit={handleSubmit} className="flex flex-col h-full min-h-0 ">
            <div className="flex-1 min-h-0 overflow-hidden px-1">
                <FieldGroup className="gap-4 flex flex-col min-h-0 h-full">
                    <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-4">
                        <Field>
                            <FieldLabel htmlFor="group-name">{nameLabel ?? t('form.name')}</FieldLabel>
                            <Input
                                id="group-name"
                                value={groupName}
                                onChange={(e) => setGroupName(e.target.value)}
                                className="rounded-xl"
                            />
                        </Field>
                        <Field>
                            <FieldLabel htmlFor="group-match-regex">{t('form.matchRegex')}</FieldLabel>
                            <Input
                                id="group-match-regex"
                                value={matchRegex}
                                onChange={(e) => setMatchRegex(e.target.value)}
                                className="rounded-xl"
                                placeholder={t('form.matchRegexPlaceholder')}
                            />
                            {regexError && (
                                <p className="mt-1 text-xs text-destructive">
                                    {t('form.matchRegexInvalid')}: {regexError}
                                </p>
                            )}
                        </Field>

                        <Field>
                            <FieldLabel htmlFor="group-first-token-time-out">
                                {t('form.firstTokenTimeOut')}
                                <TooltipProvider>
                                    <Tooltip>
                                        <TooltipTrigger asChild>
                                            <HelpCircle className="size-4 text-muted-foreground cursor-help" />
                                        </TooltipTrigger>
                                        <TooltipContent>
                                            {t('form.firstTokenTimeOutHint')}
                                        </TooltipContent>
                                    </Tooltip>
                                </TooltipProvider>
                            </FieldLabel>
                            <Input
                                id="group-first-token-time-out"
                                type="number"
                                inputMode="numeric"
                                min={0}
                                step={1}
                                value={String(firstTokenTimeOut)}
                                onChange={(e) => {
                                    const raw = e.target.value;
                                    if (raw.trim() === '') {
                                        setFirstTokenTimeOut(0);
                                        return;
                                    }
                                    const n = Number.parseInt(raw, 10);
                                    setFirstTokenTimeOut(Number.isFinite(n) && n > 0 ? n : 0);
                                }}
                                className="rounded-xl"
                            />
                        </Field>

                        <Field>
                            <FieldLabel htmlFor="group-session-keep-time">
                                {t('form.sessionKeepTime')}
                                <TooltipProvider>
                                    <Tooltip>
                                        <TooltipTrigger asChild>
                                            <HelpCircle className="size-4 text-muted-foreground cursor-help" />
                                        </TooltipTrigger>
                                        <TooltipContent>
                                            {t('form.sessionKeepTimeHint')}
                                        </TooltipContent>
                                    </Tooltip>
                                </TooltipProvider>
                            </FieldLabel>
                            <Input
                                id="group-session-keep-time"
                                type="number"
                                inputMode="numeric"
                                min={0}
                                step={1}
                                value={String(sessionKeepTime)}
                                onChange={(e) => {
                                    const raw = e.target.value;
                                    if (raw.trim() === '') {
                                        setSessionKeepTime(0);
                                        return;
                                    }
                                    const n = Number.parseInt(raw, 10);
                                    setSessionKeepTime(Number.isFinite(n) && n > 0 ? n : 0);
                                }}
                                className="rounded-xl"
                            />
                        </Field>
                    </div>

                    {/* Mode + Retry Toggle */}
                    <div className="flex items-center gap-2">
                        <div className="flex gap-1 flex-1">
                            {([1, 2, 3, 4] as const).map((m) => (
                                <button
                                    key={m}
                                    type="button"
                                    onClick={() => setMode(m)}
                                    className={cn(
                                        'flex-1 py-1 text-xs rounded-lg transition-colors',
                                        mode === m ? 'bg-primary text-primary-foreground' : 'bg-muted hover:bg-muted/80'
                                    )}
                                >
                                    {t(`mode.${MODE_LABELS[m]}`)}
                                </button>
                            ))}
                        </div>
                        <TooltipProvider>
                            <Tooltip>
                                <TooltipTrigger asChild>
                                    <label className="flex items-center gap-1.5 shrink-0 cursor-pointer">
                                        <Switch
                                            checked={retryEnabled}
                                            onCheckedChange={setRetryEnabled}
                                        />
                                        <span className="text-xs text-muted-foreground">{t('form.retryEnabled')}</span>
                                    </label>
                                </TooltipTrigger>
                                <TooltipContent>
                                    {t('form.retryEnabledHint')}
                                </TooltipContent>
                            </Tooltip>
                        </TooltipProvider>
                        {retryEnabled && (
                            <TooltipProvider>
                                <Tooltip>
                                    <TooltipTrigger asChild>
                                        <label className="flex items-center gap-1.5 shrink-0">
                                            <Input
                                                type="number"
                                                inputMode="numeric"
                                                min={1}
                                                step={1}
                                                value={String(maxRetries)}
                                                onChange={(e) => {
                                                    const raw = e.target.value;
                                                    if (raw.trim() === '') { setMaxRetries(1); return; }
                                                    const n = Number.parseInt(raw, 10);
                                                    setMaxRetries(Number.isFinite(n) && n > 0 ? n : 1);
                                                }}
                                                className="w-16 h-7 rounded-lg text-xs text-center"
                                            />
                                            <span className="text-xs text-muted-foreground">{t('form.maxRetries')}</span>
                                        </label>
                                    </TooltipTrigger>
                                    <TooltipContent>
                                        {t('form.maxRetriesHint')}
                                    </TooltipContent>
                                </Tooltip>
                            </TooltipProvider>
                        )}
                    </div>

                    <div className="flex-1 min-h-0">
                        <div className="grid grid-cols-1 md:grid-cols-2 gap-4 h-full min-h-0">
                            <ModelPickerSection
                                modelChannels={modelChannels}
                                selectedMembers={selectedMembers}
                                onAdd={handleAddMember}
                                onAutoAdd={handleAutoAdd}
                                autoAddDisabled={autoAddDisabled}
                            />
                            <SortSection
                                members={selectedMembers}
                                onReorder={setSelectedMembers}
                                onRemove={handleRemoveMember}
                                onWeightChange={handleWeightChange}
                                removingIds={removingIds}
                                showWeight={mode === 4}
                                onClear={handleClearMembers}
                            />
                        </div>
                    </div>
                </FieldGroup>
            </div>

            <div className="mt-auto shrink-0 px-1 pt-4">
                <div className="flex gap-2">
                    {onCancel && (
                        <Button type="button" variant="secondary" className="flex-1 rounded-xl h-11" onClick={onCancel}>
                            {t('detail.actions.cancel')}
                        </Button>
                    )}
                    <Button
                        type="submit"
                        disabled={!isValid || isSubmitting}
                        className="flex-1 rounded-xl h-11"
                    >
                        {isSubmitting ? submittingText : submitText}
                    </Button>
                </div>
            </div>
        </form>
    );
}
