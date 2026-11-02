'use client';

import { Children, isValidElement, type ComponentProps, type ReactNode } from 'react';
import { motion, useReducedMotion } from 'motion/react';
import { useTabs } from '@/components/animate-ui/primitives/animate/tabs';
import { cn } from '@/lib/utils';

const PANEL_TRANSITION = {
    type: 'tween',
    duration: 0.36,
    ease: [0.22, 1, 0.36, 1],
} as const;

/** 配置页共用的翻页视窗：只移动正文，标题与保存区保持固定。 */
export function AgentConfigPanels({ children }: { children: ReactNode }) {
    const { activeValue } = useTabs();
    const reducedMotion = useReducedMotion();
    const panels = Children.toArray(children);
    const activeIndex = panels.findIndex((panel) => (
        isValidElement<{ value: string }>(panel) && panel.props.value === activeValue
    ));

    return (
        <div data-slot="agent-config-viewport" className="h-96 min-h-0 flex-auto overflow-hidden">
            <motion.div
                data-slot="agent-config-track"
                className="flex h-full w-full"
                initial={false}
                animate={{ x: `${Math.max(activeIndex, 0) * -100}%` }}
                transition={reducedMotion ? { duration: 0 } : PANEL_TRANSITION}
            >
                {panels}
            </motion.div>
        </div>
    );
}

type AgentConfigPanelProps = ComponentProps<'section'> & { value: string; scrollable?: boolean };

export function AgentConfigPanel({ value, scrollable = true, className, ...props }: AgentConfigPanelProps) {
    const { activeValue } = useTabs();
    const active = activeValue === value;

    return (
        <section
            {...props}
            data-slot="agent-config-panel"
            data-state={active ? 'active' : 'inactive'}
            role="tabpanel"
            inert={!active}
            aria-hidden={!active}
            className={cn('h-full min-h-0 w-full shrink-0 px-1', scrollable ? 'overflow-y-auto' : 'overflow-hidden', className)}
        />
    );
}
