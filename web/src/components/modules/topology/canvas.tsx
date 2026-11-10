'use client';

import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useTranslations } from 'next-intl';
import { gsap } from 'gsap';
import { Draggable } from 'gsap/Draggable';
import { MotionPathPlugin } from 'gsap/MotionPathPlugin';
import Logo from '@/components/modules/logo';
import { useGroupList, type Group } from '@/api/endpoints/group';
import { isTopologyNodeActive, type TopologyLiveView } from '@/api/endpoints/topology';
import { cn } from '@/lib/utils';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/animate-ui/components/animate/tooltip';
import {
    MorphingDialog,
    MorphingDialogContainer,
    MorphingDialogTrigger,
} from '@/components/ui/morphing-dialog';
import { FileCog } from 'lucide-react';
import { AgentIcon, agentDisplayName } from './AgentIcon';
import { AgentConfigDialogPanel } from './AgentConfigDialog';

gsap.registerPlugin(Draggable, MotionPathPlugin);

// 画布上最多展示的分组卡片数（超出时优先展示活跃/最近调用过的）
const GROUP_CARD_LIMIT = 8;
// 网关节点的拖拽/偏移键名
const GATEWAY_KEY = 'gateway';
// 拖拽偏移的本地持久化键（切页面/刷新后保留用户手动布局）
const OFFSETS_STORAGE_KEY = 'octopus.topology.offsets.v1';
// 「编辑配置」弹窗的形变弹簧。相比最初的 stiffness:480 / damping:30 放慢约 68%，
// 按同比例下调 damping 以保持原阻尼比（ζ≈0.68）：只改速度，不改手感。
const CONFIG_DIALOG_TRANSITION = { type: 'spring', stiffness: 170, damping: 18 } as const;

function prefersReducedMotion(): boolean {
    return typeof window !== 'undefined' && window.matchMedia('(prefers-reduced-motion: reduce)').matches;
}

interface NodeOffset {
    dx: number;
    dy: number;
}

function loadOffsets(): Record<string, NodeOffset> {
    if (typeof window === 'undefined') return {};
    try {
        const raw = window.localStorage.getItem(OFFSETS_STORAGE_KEY);
        if (!raw) return {};
        const parsed = JSON.parse(raw) as Record<string, NodeOffset>;
        if (!parsed || typeof parsed !== 'object') return {};
        // 安装态与调用态使用同一节点身份，迁移旧偏移以保留用户布局。
        const normalized = { ...parsed };
        for (const [key, offset] of Object.entries(parsed)) {
            if (!key.startsWith('caller:') && !key.startsWith('installed:')) continue;
            const stable = `agent:${key.slice(key.indexOf(':') + 1)}`;
            if (!parsed[stable] && (key.startsWith('caller:') || !normalized[stable])) normalized[stable] = offset;
            delete normalized[key];
        }
        return normalized;
    } catch {
        return {};
    }
}

function saveOffsets(offsets: Record<string, NodeOffset>) {
    try {
        window.localStorage.setItem(OFFSETS_STORAGE_KEY, JSON.stringify(offsets));
    } catch {
        // 隐私模式等存储不可用时静默降级为仅会话内生效
    }
}

type LinkStatus = 'active' | 'idle' | 'dim';

interface AgentNode {
    key: string;
    agent: string;
    title: string;
    subtitle?: string;
    status: LinkStatus;
    anyActive: boolean;
    lastSeen?: number;
    hasConfig: boolean;
}

interface GroupNode {
    key: string;
    title: string;
    subtitle?: string;
    status: LinkStatus;
    anyActive: boolean;
}

interface NodeAnchor {
    x1: number;
    y1: number;
}

type LinkAxis = 'horizontal' | 'vertical';

interface GatewayAnchor {
    incoming: NodeAnchor;
    outgoing: NodeAnchor;
    axis: LinkAxis;
}

function linkPath(from: NodeAnchor, to: NodeAnchor, axis: LinkAxis): string {
    // 连线方向跟随布局与锚点朝向，不能由两个节点的距离重新猜测。
    if (axis === 'horizontal') {
        const handle = Math.max(Math.abs(to.x1 - from.x1) / 2, 24);
        return `M ${from.x1} ${from.y1} C ${from.x1 + handle} ${from.y1}, ${to.x1 - handle} ${to.y1}, ${to.x1} ${to.y1}`;
    }
    const handle = Math.max(Math.abs(to.y1 - from.y1) / 2, 24);
    return `M ${from.x1} ${from.y1} C ${from.x1} ${from.y1 + handle}, ${to.x1} ${to.y1 - handle}, ${to.x1} ${to.y1}`;
}

function TopologyPath({ d, status, dot }: { d: string; status: LinkStatus; dot: boolean }) {
    const flowRef = useRef<SVGPathElement>(null);
    const dotRef = useRef<SVGCircleElement>(null);

    // 活跃链路的流光：虚线沿路径方向循环流动（GSAP 驱动 stroke-dashoffset）
    useEffect(() => {
        if (status !== 'active' || !flowRef.current || prefersReducedMotion()) return;
        const tween = gsap.fromTo(
            flowRef.current,
            { strokeDashoffset: 0 },
            { strokeDashoffset: -16, duration: 0.9, ease: 'none', repeat: -1 },
        );
        return () => {
            tween.kill();
        };
    }, [status]);

    // 光点沿贝塞尔路径运动（方向：agent→网关 / 网关→分组）
    useEffect(() => {
        if (status !== 'active' || !dot || !dotRef.current || prefersReducedMotion()) return;
        const tween = gsap.to(dotRef.current, {
            duration: 1.8,
            repeat: -1,
            ease: 'none',
            motionPath: { path: d },
        });
        return () => {
            tween.kill();
        };
    }, [status, dot, d]);

    return (
        <g data-slot="topology-link" data-status={status}>
            <path
                d={d}
                fill="none"
                strokeDasharray={status === 'active' ? undefined : '5 7'}
                className={cn(
                    'topology-line',
                    status === 'dim' && 'topology-line-dim',
                    status === 'active' && 'topology-line-track',
                )}
            />
            {status === 'active' && (
                <path
                    ref={flowRef}
                    d={d}
                    fill="none"
                    strokeDasharray="6 10"
                    className="topology-line-active"
                />
            )}
            {dot && status === 'active' && <circle ref={dotRef} r={3} className="topology-line-dot" />}
        </g>
    );
}

export function TopologyCanvas({ view }: { view: TopologyLiveView }) {
    const t = useTranslations('topology');
    const groupsQuery = useGroupList();
    const groups: Group[] = useMemo(() => groupsQuery.data ?? [], [groupsQuery.data]);

    const containerRef = useRef<HTMLDivElement>(null);
    const layoutRef = useRef<HTMLDivElement>(null);
    const gatewayRef = useRef<HTMLDivElement>(null);
    const gatewayColRef = useRef<HTMLDivElement | null>(null);
    const agentsColRef = useRef<HTMLDivElement>(null);
    const groupsColRef = useRef<HTMLDivElement>(null);
    const agentRefs = useRef<Map<string, HTMLElement>>(new Map());
    const groupRefs = useRef<Map<string, HTMLElement>>(new Map());
    const [gatewayAnchor, setGatewayAnchor] = useState<GatewayAnchor | null>(null);
    const [agentAnchors, setAgentAnchors] = useState<Record<string, NodeAnchor>>({});
    const [groupAnchors, setGroupAnchors] = useState<Record<string, NodeAnchor>>({});

    // 用户手动拖拽的节点偏移（相对默认布局位置），localStorage 持久化；
    // 元素 transform 由 GSAP/Draggable 直接持有，React state 只负责连线端点与持久化
    const [offsets, setOffsets] = useState<Record<string, NodeOffset>>(loadOffsets);
    const [draggingKey, setDraggingKey] = useState<string | null>(null);
    const offsetsRef = useRef(offsets);

    useEffect(() => {
        offsetsRef.current = offsets;
    }, [offsets]);

    useEffect(() => {
        saveOffsets(offsets);
    }, [offsets]);

    // 复原布局：所有带偏移的节点以弹性缓动飞回默认位置，连线实时跟随
    const resetLayout = useCallback(() => {
        const collect = (): Array<[string, HTMLElement]> => {
            const entries: Array<[string, HTMLElement]> = [];
            agentRefs.current.forEach((el, key) => entries.push([key, el]));
            groupRefs.current.forEach((el, key) => entries.push([key, el]));
            const gatewayEl = gatewayColRef.current;
            if (gatewayEl) entries.push([GATEWAY_KEY, gatewayEl]);
            return entries;
        };
        collect().forEach(([key, el]) => {
            if (!offsetsRef.current[key]) return;
            gsap.killTweensOf(el);
            gsap.to(el, {
                x: 0,
                y: 0,
                duration: 0.7,
                ease: 'back.out(1.6)',
                onUpdate() {
                    const dx = Number(gsap.getProperty(el, 'x')) || 0;
                    const dy = Number(gsap.getProperty(el, 'y')) || 0;
                    setOffsets((prev) => ({ ...prev, [key]: { dx, dy } }));
                },
                onComplete() {
                    setOffsets((prev) => {
                        if (!(key in prev)) return prev;
                        const next = { ...prev };
                        delete next[key];
                        return next;
                    });
                },
            });
        });
    }, []);

    const resetLayoutRef = useRef(resetLayout);
    useEffect(() => {
        resetLayoutRef.current = resetLayout;
    }, [resetLayout]);

    // GSAP Draggable 管拖拽生命周期（指针捕获/触摸/点击判定均由它处理）。
    // translate 由 React 以 inline style 渲染（重挂载必然恢复，无竞态），
    // GSAP 只持有内层包裹的 scale —— 两层 transform 分离，互不覆盖。
    const createDraggable = useCallback((key: string, el: HTMLElement) => {
        if (Draggable.get(el)) return;
        const inner = el.firstElementChild as HTMLElement | null;
        Draggable.create(el, {
            type: 'x,y',
            // 不用 Draggable 的 zIndex 选项：它写入的内联 z-index 在松手后不回收，
            // 拖过的卡片会永久浮在 z-50 的模态框遮罩之上；层级提升改用 draggingKey 状态类
            bounds: containerRef.current ?? undefined,
            onPress() {
                setDraggingKey(key);
                // 拿起：轻微放大，营造"提起"手感
                if (inner && !prefersReducedMotion()) {
                    gsap.to(inner, { scale: 1.03, duration: 0.18, ease: 'power2.out' });
                }
            },
            onDrag() {
                // Draggable 基于 GSAP transform 缓存，getProperty 即当前实时位置
                const dx = Number(gsap.getProperty(el, 'x')) || 0;
                const dy = Number(gsap.getProperty(el, 'y')) || 0;
                setOffsets((prev) => ({ ...prev, [key]: { dx, dy } }));
            },
            onRelease() {
                setDraggingKey(null);
                el.style.zIndex = '';
                // 放下：带回弹的缩放回落
                if (inner && !prefersReducedMotion()) {
                    gsap.to(inner, { scale: 1, duration: 0.5, ease: 'back.out(2.2)' });
                }
            },
            onClick() {
                // 未产生位移的点击：网关节点 = 复原布局
                if (key === GATEWAY_KEY) resetLayoutRef.current();
            },
        });
    }, []);

    // 左列：每个 agent 类型一张卡（同一 agent 多把密钥时取最近调用的密钥做副标题）
    const agentNodes = useMemo<AgentNode[]>(() => {
        const nodes = new Map<string, AgentNode>();
        const hasConfigByAgent = new Map<string, boolean>();
        for (const installed of view.installed ?? []) {
            hasConfigByAgent.set(installed.agent, installed.has_config);
        }
        for (const caller of view.callers) {
            const existing = nodes.get(caller.agent);
            if (existing) {
                if (caller.last_seen > (existing.lastSeen ?? 0)) {
                    existing.lastSeen = caller.last_seen;
                    existing.subtitle = caller.api_key_name;
                }
                existing.anyActive = existing.anyActive || caller.active > 0;
                continue;
            }
            nodes.set(caller.agent, {
                key: `agent:${caller.agent}`,
                agent: caller.agent,
                title: agentDisplayName(caller.agent),
                subtitle: caller.api_key_name,
                status: isTopologyNodeActive(caller.active) ? 'active' : 'idle',
                anyActive: caller.active > 0,
                lastSeen: caller.last_seen,
                hasConfig: hasConfigByAgent.get(caller.agent) ?? false,
            });
        }
        // 已安装但没调用过的 agent → 灰卡常驻（效果图左侧的空闲卡）
        for (const installed of view.installed ?? []) {
            if (!installed.installed || nodes.has(installed.agent)) continue;
            nodes.set(installed.agent, {
                key: `agent:${installed.agent}`,
                agent: installed.agent,
                title: agentDisplayName(installed.agent),
                subtitle: undefined,
                status: 'dim',
                anyActive: false,
                hasConfig: installed.has_config,
            });
        }
        return [...nodes.values()];
    }, [view]);

    // 右列：被调用的分组（活跃优先）+ 配置中的分组填充灰卡
    const groupNodes = useMemo<GroupNode[]>(() => {
        const called = new Map<string, GroupNode & { lastSeen: number }>();
        for (const caller of view.callers) {
            for (const link of caller.groups) {
                const key = String(link.group_id || link.group_name);
                const active = isTopologyNodeActive(link.active);
                const existing = called.get(key);
                if (!existing) {
                    called.set(key, {
                        key,
                        title: link.group_name,
                        subtitle: link.protocol,
                        status: active ? 'active' : 'idle',
                        anyActive: link.active > 0,
                        lastSeen: link.last_seen,
                    });
                    continue;
                }
                existing.anyActive = existing.anyActive || link.active > 0;
                if (active) existing.status = 'active';
                if (link.last_seen > existing.lastSeen) {
                    existing.lastSeen = link.last_seen;
                    existing.subtitle = link.protocol;
                }
            }
        }
        const result: GroupNode[] = [...called.values()].sort((a, b) => {
            if (a.status !== b.status) return a.status === 'active' ? -1 : 1;
            return b.lastSeen - a.lastSeen;
        });
        const calledKeys = new Set(called.keys());
        for (const group of groups) {
            if (result.length >= GROUP_CARD_LIMIT) break;
            const key = String(group.id ?? group.name);
            if (calledKeys.has(key) || calledKeys.has(group.name)) continue;
            result.push({
                key,
                title: group.name,
                subtitle: undefined,
                status: 'dim',
                anyActive: false,
            });
        }
        return result.slice(0, GROUP_CARD_LIMIT);
    }, [view, groups]);

    // 用 offsetLeft/offsetTop 计算相对容器的布局位置（不含 CSS transform）。
    // 不能用 getBoundingClientRect：外层 AnimatePresence 入场动画（scale/translate）
    // 进行中挂载时它会把 transform 算进坐标，而 transform 不触发 ResizeObserver，
    // 锚点会永远停留在动画中间态的错位位置（切页返回时必现）。
    const measure = useCallback(() => {
        const container = containerRef.current;
        const layout = layoutRef.current;
        if (!container || !layout) return;
        const columns = window.getComputedStyle(layout).gridTemplateColumns.trim().split(/\s+/);
        const axis: LinkAxis = columns.length > 1 ? 'horizontal' : 'vertical';
        const horizontal = axis === 'horizontal';
        const anchorAt = (el: HTMLElement, side: 'left' | 'right' | 'top' | 'bottom'): NodeAnchor => {
            let x = 0;
            let y = 0;
            let cur: HTMLElement | null = el;
            while (cur && cur !== container) {
                x += cur.offsetLeft;
                y += cur.offsetTop;
                cur = cur.offsetParent as HTMLElement | null;
            }
            if (side === 'top' || side === 'bottom') {
                return { x1: x + el.offsetWidth / 2, y1: side === 'top' ? y : y + el.offsetHeight };
            }
            return { x1: side === 'left' ? x : x + el.offsetWidth, y1: y + el.offsetHeight / 2 };
        };
        const gatewayEl = gatewayRef.current;
        setGatewayAnchor(
            gatewayEl
                ? {
                    incoming: anchorAt(gatewayEl, horizontal ? 'left' : 'top'),
                    // 竖排时从整个网关节点下方出线，避开 Logo 下方的标题和标签。
                    outgoing: anchorAt(horizontal ? gatewayEl : (gatewayColRef.current ?? gatewayEl), horizontal ? 'right' : 'bottom'),
                    axis,
                }
                : null,
        );
        const nextAgents: Record<string, NodeAnchor> = {};
        agentRefs.current.forEach((el, key) => {
            nextAgents[key] = anchorAt(el, horizontal ? 'right' : 'bottom');
        });
        setAgentAnchors(nextAgents);
        const nextGroups: Record<string, NodeAnchor> = {};
        groupRefs.current.forEach((el, key) => {
            nextGroups[key] = anchorAt(el, horizontal ? 'left' : 'top');
        });
        setGroupAnchors(nextGroups);

        // 钳制越界的存量偏移：旧版本记录的偏移可能把节点推到画布外（被 overflow-hidden
        // 裁掉），测量时按容器边界收紧并把修正值写回 state/localStorage，实现自愈
        const containerW = container.clientWidth;
        const containerH = container.clientHeight;
        const clampIntoView = (refs: Map<string, HTMLElement>) => {
            refs.forEach((el, key) => {
                const off = offsetsRef.current[key];
                if (!off) return;
                let bx = 0;
                let by = 0;
                let cur: HTMLElement | null = el;
                while (cur && cur !== container) {
                    bx += cur.offsetLeft;
                    by += cur.offsetTop;
                    cur = cur.offsetParent as HTMLElement | null;
                }
                const minX = -bx;
                const maxX = containerW - el.offsetWidth - bx;
                const minY = -by;
                const maxY = containerH - el.offsetHeight - by;
                const dx = Math.min(Math.max(off.dx, minX), Math.max(minX, maxX));
                const dy = Math.min(Math.max(off.dy, minY), Math.max(minY, maxY));
                if (dx !== off.dx || dy !== off.dy) {
                    setOffsets((prev) => ({ ...prev, [key]: { dx, dy } }));
                }
            });
        };
        clampIntoView(agentRefs.current);
        clampIntoView(groupRefs.current);
        if (gatewayColRef.current) clampIntoView(new Map([[GATEWAY_KEY, gatewayColRef.current]]));
    }, []);

    // 数据变化（卡片增减）后补一次测量。用 setTimeout 而非 rAF：页面在后台/被遮挡时
    // rAF 永远不会触发，越界偏移的钳制会一直不生效；setTimeout 在任何可见性下都会执行
    useEffect(() => {
        const timer = setTimeout(measure, 0);
        return () => clearTimeout(timer);
    }, [measure, agentNodes, groupNodes]);

    // ResizeObserver 在 observe 时会立即回调一次（覆盖首次测量），
    // 之后容器/两列尺寸变化（卡片增减、窗口缩放）都会重新测量
    useEffect(() => {
        const observer = new ResizeObserver(() => measure());
        if (containerRef.current) observer.observe(containerRef.current);
        if (layoutRef.current) observer.observe(layoutRef.current);
        if (agentsColRef.current) observer.observe(agentsColRef.current);
        if (groupsColRef.current) observer.observe(groupsColRef.current);
        window.addEventListener('resize', measure);
        return () => {
            observer.disconnect();
            window.removeEventListener('resize', measure);
        };
    }, [measure]);

    // 首次出现的节点做弹入动画（错落节奏）。只缩放内层包裹，不碰外层 translate。
    const seenNodesRef = useRef<Set<string>>(new Set());
    useEffect(() => {
        const reduced = prefersReducedMotion();
        const animate = (key: string, el: HTMLElement, delay: number) => {
            if (seenNodesRef.current.has(key)) return delay;
            seenNodesRef.current.add(key);
            const inner = el.firstElementChild as HTMLElement | null;
            if (reduced || !inner) return delay;
            gsap.fromTo(
                inner,
                { scale: 0.88 },
                { scale: 1, duration: 0.35, ease: 'power2.out', delay },
            );
            return delay + 0.04;
        };
        let delay = 0;
        agentRefs.current.forEach((el, key) => {
            delay = animate(key, el, delay);
        });
        const gatewayEl = gatewayColRef.current;
        if (gatewayEl) {
            delay = animate(GATEWAY_KEY, gatewayEl, delay);
        }
        groupRefs.current.forEach((el, key) => {
            delay = animate(key, el, delay);
        });
    }, [agentNodes, groupNodes]);

    // 连线锚点 = 布局锚点 + 用户拖拽偏移
    const shifted = (key: string, anchor: NodeAnchor): NodeAnchor => {
        const offset = offsets[key];
        return offset ? { x1: anchor.x1 + offset.dx, y1: anchor.y1 + offset.dy } : anchor;
    };

    // 外层节点由 React 渲染 translate（重挂载/刷新后必然恢复），GSAP/Draggable 只在同值覆写
    const nodeTranslateStyle = (key: string) => {
        const offset = offsets[key];
        return offset ? { transform: `translate3d(${offset.dx}px, ${offset.dy}px, 0)` } : undefined;
    };

    const anyActive =
        agentNodes.some((node) => node.anyActive) || groupNodes.some((node) => node.anyActive);

    const cardTone = (status: LinkStatus) =>
        cn(
            'flex min-w-0 items-center gap-3 rounded-2xl border bg-card px-4 py-3 shadow-sm transition-colors',
            status === 'dim' && 'opacity-55',
            status === 'active' && 'border-primary/40',
        );

    return (
        <div ref={containerRef} className="relative h-full min-h-0 w-full overflow-hidden">
            <svg data-slot="topology-connections" className="pointer-events-none absolute inset-0 h-full w-full">
                {agentNodes.map((node) => {
                    const from = agentAnchors[node.key];
                    if (!from || !gatewayAnchor) return null;
                    return (
                        <TopologyPath
                            key={node.key}
                            d={linkPath(shifted(node.key, from), shifted(GATEWAY_KEY, gatewayAnchor.incoming), gatewayAnchor.axis)}
                            status={node.status}
                            dot={node.anyActive}
                        />
                    );
                })}
                {groupNodes.map((node) => {
                    const to = groupAnchors[node.key];
                    if (!to || !gatewayAnchor) return null;
                    return (
                        <TopologyPath
                            key={node.key}
                            d={linkPath(shifted(GATEWAY_KEY, gatewayAnchor.outgoing), shifted(node.key, to), gatewayAnchor.axis)}
                            status={node.status}
                            dot={node.anyActive}
                        />
                    );
                })}
            </svg>

            <div ref={layoutRef} className="relative grid h-full grid-cols-1 grid-rows-[auto_auto_auto] items-center gap-4 p-4 md:grid-cols-[minmax(0,1fr)_auto_minmax(0,1fr)] md:grid-rows-1 md:gap-8 md:p-6">
                {/* 左列：调用方 agent 卡片（桌面端贴左缘，与右列对称，网关居中） */}
                <div
                    ref={agentsColRef}
                    className="flex flex-row flex-wrap items-stretch justify-center gap-3 md:flex-col md:flex-nowrap md:items-start"
                >
                    {agentNodes.map((node) => (
                        <div
                            key={node.key}
                            ref={(el) => {
                                // 卸载时绝不 kill Draggable：内联 ref 每次渲染都会以 null 重挂，
                                // 若在这里 kill 会把正在拖拽手势中的实例杀掉（表现为要点一下才能拖）。
                                // Draggable.get 幂等守卫保证实例只会创建一次，随元素一起被 GC。
                                if (el) {
                                    agentRefs.current.set(node.key, el);
                                    createDraggable(node.key, el);
                                } else {
                                    agentRefs.current.delete(node.key);
                                }
                            }}
                            style={nodeTranslateStyle(node.key)}
                            className={cn(
                                'w-52 cursor-grab touch-none select-none',
                                draggingKey === node.key && 'z-20 cursor-grabbing',
                            )}
                        >
                            <div
                                className={cn(
                                    cardTone(node.status),
                                    'w-full',
                                    draggingKey === node.key && 'shadow-lg',
                                )}
                            >
                                <AgentIcon agent={node.agent} className="size-5 shrink-0" />
                                <div className="min-w-0 flex-1">
                                    <div className="truncate text-sm font-semibold text-card-foreground">
                                        {node.title}
                                    </div>
                                    {node.subtitle && (
                                        <div className="truncate text-xs text-muted-foreground">
                                            {node.subtitle}
                                        </div>
                                    )}
                                </div>
                                {node.hasConfig && (
                                    <MorphingDialog
                                        transition={CONFIG_DIALOG_TRANSITION}
                                    >
                                        <MorphingDialogTrigger
                                            className="rounded-lg p-1 text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
                                            aria-label={t('editConfig')}
                                        >
                                            <Tooltip side="top" sideOffset={8}>
                                                <TooltipTrigger asChild>
                                                    <span
                                                        className="flex"
                                                        onPointerDown={(e) => e.stopPropagation()}
                                                    >
                                                        <FileCog className="size-4" />
                                                    </span>
                                                </TooltipTrigger>
                                                <TooltipContent>{t('editConfig')}</TooltipContent>
                                            </Tooltip>
                                        </MorphingDialogTrigger>
                                        <MorphingDialogContainer>
                                            <AgentConfigDialogPanel agent={node.agent} transition={CONFIG_DIALOG_TRANSITION} />
                                        </MorphingDialogContainer>
                                    </MorphingDialog>
                                )}
                                {node.anyActive && (
                                    <span className="size-2 shrink-0 animate-pulse rounded-full bg-emerald-500" />
                                )}
                            </div>
                        </div>
                    ))}
                </div>

                {/* 中央：网关节点（可拖拽；未产生位移的点击 = 复原布局） */}
                <div
                    ref={(el) => {
                        if (!el) {
                            gatewayColRef.current = null;
                            return;
                        }
                        gatewayColRef.current = el;
                        createDraggable(GATEWAY_KEY, el);
                    }}
                    style={nodeTranslateStyle(GATEWAY_KEY)}
                    className={cn(
                        'cursor-grab touch-none select-none',
                        draggingKey === GATEWAY_KEY && 'z-20 cursor-grabbing',
                    )}
                >
                    <div className="flex flex-col items-center justify-center gap-2.5 py-2">
                        <Tooltip side="bottom" sideOffset={8}>
                            <TooltipTrigger asChild>
                                <div
                                    ref={gatewayRef}
                                    role="button"
                                    tabIndex={0}
                                    aria-label={t('reset')}
                                    onKeyDown={(e) => {
                                        if (e.key === 'Enter' || e.key === ' ') resetLayoutRef.current();
                                    }}
                                    className="relative rounded-3xl border border-border bg-card p-4 shadow-sm"
                                >
                                    {/* Logo 始终完整显示：Logo 组件的 animate 模式是"描画-淡出"循环，
                                        会周期性整只消失，不适合表达活跃态；活跃感用呼吸光环表达 */}
                                    <Logo size={56} />
                                    {anyActive && (
                                        <div className="pointer-events-none absolute -inset-px animate-pulse rounded-3xl ring-2 ring-primary/30" />
                                    )}
                                </div>
                            </TooltipTrigger>
                            <TooltipContent>{t('reset')}</TooltipContent>
                        </Tooltip>
                        <span className="text-sm font-semibold text-card-foreground">octopus</span>
                        <span className="rounded-full border border-border bg-card px-3 py-0.5 text-xs text-muted-foreground">
                            {t('gateway')}
                        </span>
                    </div>
                </div>

                {/* 右列：分组卡片（桌面端贴右缘，与左列对称） */}
                <div
                    ref={groupsColRef}
                    className="flex flex-row flex-wrap items-stretch justify-center gap-3 md:flex-col md:flex-nowrap md:items-end"
                >
                    {groupNodes.map((node) => (
                        <div
                            key={node.key}
                            ref={(el) => {
                                if (el) {
                                    groupRefs.current.set(node.key, el);
                                    createDraggable(node.key, el);
                                } else {
                                    groupRefs.current.delete(node.key);
                                }
                            }}
                            style={nodeTranslateStyle(node.key)}
                            className={cn(
                                'w-52 cursor-grab touch-none select-none',
                                draggingKey === node.key && 'z-20 cursor-grabbing',
                            )}
                        >
                            <div
                                className={cn(
                                    cardTone(node.status),
                                    'w-full',
                                    draggingKey === node.key && 'shadow-lg',
                                )}
                            >
                                <span
                                    className={cn(
                                        'size-2 shrink-0 rounded-full',
                                        node.anyActive
                                            ? 'animate-pulse bg-emerald-500'
                                            : node.status === 'idle'
                                                ? 'bg-muted-foreground/40'
                                                : 'bg-border',
                                    )}
                                />
                                <div className="min-w-0 flex-1">
                                    <div className="truncate text-sm font-semibold text-card-foreground">
                                        {node.title}
                                    </div>
                                    {node.subtitle && (
                                        <div className="truncate text-xs text-muted-foreground">
                                            {node.subtitle}
                                        </div>
                                    )}
                                </div>
                            </div>
                        </div>
                    ))}
                </div>
            </div>
        </div>
    );
}
