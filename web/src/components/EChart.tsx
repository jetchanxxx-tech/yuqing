import { useEffect, useRef, useState } from 'react';
import type { EChartsOption } from 'echarts';

/**
 * 轻量 echarts React 封装：动态加载按需注册的 echarts / 初始化 / 自适应 resize / 卸载销毁。
 * option 类型为 type-only 引用，不会增加运行时体积。
 */

interface ChartHandle {
  setOption(option: EChartsOption, opts?: { notMerge?: boolean }): void;
  resize(): void;
  dispose(): void;
}

interface EChartModule {
  init(el: HTMLElement): ChartHandle;
}

let loadEchartsPromise: Promise<EChartModule> | null = null;
function loadEcharts(): Promise<EChartModule> {
  loadEchartsPromise ??= import('../lib/echarts').then((m) => m.default as unknown as EChartModule);
  return loadEchartsPromise;
}

interface EChartProps {
  option: EChartsOption;
  height?: number | string;
  /** 数据为空时不渲染图形 */
  isEmpty?: boolean;
}

export default function EChart({ option, height = 300, isEmpty = false }: EChartProps) {
  const containerRef = useRef<HTMLDivElement>(null);
  const chartRef = useRef<ChartHandle | null>(null);
  const [echartsReady, setEchartsReady] = useState(false);

  // 预加载 echarts 运行时（异步块）
  useEffect(() => {
    let cancelled = false;
    void loadEcharts().then(() => {
      if (!cancelled) setEchartsReady(true);
    });
    return () => {
      cancelled = true;
    };
  }, []);

  // 初始化 + 数据更新（option 变化时重建，保证与最新数据一致）
  useEffect(() => {
    const el = containerRef.current;
    if (!el || !echartsReady || isEmpty) return;

    let cancelled = false;
    let ro: ResizeObserver | null = null;

    void loadEcharts().then((m) => {
      if (cancelled || !containerRef.current) return;
      const chart = m.init(containerRef.current);
      chart.setOption(option, { notMerge: true });
      chartRef.current = chart;
      ro = new ResizeObserver(() => chart.resize());
      ro.observe(containerRef.current);
    });

    return () => {
      cancelled = true;
      ro?.disconnect();
      ro = null;
      chartRef.current?.dispose();
      chartRef.current = null;
    };
  }, [option, echartsReady, isEmpty]);

  return <div ref={containerRef} style={{ width: '100%', height }} />;
}
