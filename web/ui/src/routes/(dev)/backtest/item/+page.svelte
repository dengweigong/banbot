<script lang="ts">
  import { page } from '$app/state';
  import { onMount } from 'svelte';
  import { getApi } from '$lib/netio';
  import { alerts } from "$lib/stores/alerts";
  import CodeMirror from '$lib/dev/CodeMirror.svelte';
  import { oneDark } from '@codemirror/theme-one-dark';
  import type { Extension } from '@codemirror/state';
  import * as m from '$lib/paraglide/messages.js';
  import Chart from '$lib/kline/Chart.svelte';
  import { site } from '$lib/stores/site';
  import { showPairs } from '$lib/dev/common';
  import type { BacktestDetail, BackTestTask, ExSymbol } from '$lib/dev/common';
  import { OrderDetail, type InOutOrder } from '$lib/order';
  import { TreeView, type Tree, type Node, buildTree } from '$lib/treeview';
	import { writable } from 'svelte/store';
  import RangeSlider from '$lib/dev/RangeSlider.svelte';
  import { ChartCtx, ChartSave } from '$lib/kline/chart';
  import { persisted } from 'svelte-persisted-store';
  import { makePeriod } from '$lib/kline/coms';
  import { derived } from 'svelte/store';
  import { fmtDateStr, fmtDuration, curTZ } from '$lib/dateutil';
  import type { OverlayCreate } from 'klinecharts';
  import type { TradeInfo } from '$lib/kline/types';
  import { pagination, orderCard } from '$lib/Snippets.svelte';
  import {getFirstValid} from "$lib/common";

  let id = $state('');
  let btPath = $state('');
  let detail = $state<BacktestDetail | null>(null);
  let activeTab = $state('overview');
  let theme: Extension = oneDark;
  let configText = $state('');
  let editor: CodeMirror | null = $state(null);
  let task = $state<BackTestTask | null>(null);
  let exsMap = $state<Record<string, ExSymbol> | null>(null);

  // 策略代码相关状态
  let treeData = writable<Tree>({ children: [] });
  let treeActive = $state('');
  let stratCodeContent = $state('');

  // 订单列表相关状态
  let orders = $state<InOutOrder[]>([]);
  let pairOrders = $state<InOutOrder[]>([]);
  let total = $state(0);
  let currentPage = $state(1);
  let pageSize = $state(20);
  let symbol = $state('');
  let strategy = $state('');
  let enterTag = $state('');
  let exitTag = $state('');
  let startMS = $state(0);
  let endMS = $state(0);
  let odNums = $state<number[]>();

  // 日志相关状态
  let logs = $state('');
  let logStart = $state(-1);
  let loadingLogs = $state(false);
  let assetsUrl = $derived.by(() => {
    return `${$site.apiHost}/api/dev/bt_html?task_id=${id}&type=assets`;
  })
  let entersUrl = $derived.by(() => {
    return `${$site.apiHost}/api/dev/bt_html?task_id=${id}&type=enters`;
  })

  // K线图相关状态
  let detailOrder = $state<InOutOrder | null>(null);
  let drawOrder = $state<InOutOrder | null>(null);
  let clickOrder = $state<number>(0);
  let timeRange = $state({ start: 0, end: 300 });
  let showOrderModal = $state(false);
  let drawMultiple = $state(true);
  const TRADE_GROUP = 'ban_trades';
  
  const kcCtx = writable<ChartCtx>(new ChartCtx());
  let saveRaw = new ChartSave();
  saveRaw.key = 'chart';
  const kcSave = persisted(saveRaw.key, saveRaw);
  let kc = $state<Chart|null>(null);


  let activeGroupTab = $state('pairs'); // 添加分组tab状态

  onMount(async () => {
    id = page.url.searchParams.get('id') || '';
    await loadDetail();
    if(task?.status !== 3) {
      setActiveTab('logs');
    }
    if(activeTab === 'strat_code') {
      await loadStratTree();
    }
  });

  async function loadDetail() {
    const rsp = await getApi('/dev/bt_detail', { task_id: id });
    if(rsp.code != 200) {
      alerts.error(rsp.msg || 'load detail failed');
      return;
    }
    console.log('load task detail', rsp);
    btPath = rsp.path;
    detail = rsp.detail;
    task = rsp.task;
    exsMap = rsp.exsMap;
    if(detail) {
      odNums = detail.plots.odNum;
    }else{
      odNums = [];
      setActiveTab('logs');
    }
  }

  async function loadOrders() {
    const rsp = await getApi('/dev/bt_orders', {
      task_id: id,
      page: currentPage,
      page_size: pageSize,
      symbol,
      strategy,
      enter_tag: enterTag,
      exit_tag: exitTag,
      start_ms: startMS,
      end_ms: endMS
    });
    if(rsp.code != 200) {
      alerts.error(rsp.msg || 'load orders failed');
      return;
    }
    orders = rsp.orders;
    total = rsp.total;
  }

  async function loadConfig() {
    const rsp = await getApi('/dev/bt_config', { task_id: id });
    if(rsp.code != 200) {
      alerts.error(rsp.msg || 'load config failed');
      return;
    }
    configText = rsp.data;
    if(editor) {
      editor.setValue('config.yml', configText);
    }
  }

  async function loadLogs(refresh = false) {
    if(loadingLogs) return;
    if(refresh) {
      logStart = -1;
    }
    if(logStart === 0) {
      alerts.info(m.no_more_logs());
      return;
    }
    loadingLogs = true;
    const rsp = await getApi('/dev/bt_logs', {
      task_id: id,
      end: logStart,
      limit: 20480
    });
    loadingLogs = false;
    if(rsp.code != 200) {
      console.error('load logs failed', rsp);
      alerts.error(rsp.msg || 'load logs failed');
      return;
    }
    if(refresh) {
      logs = rsp.data;
    }else{
      logs = rsp.data + logs;
    }
    logStart = rsp.start;
  }

  async function loadStratTree() {
    const rsp = await getApi('/dev/bt_strat_tree', { task_id: id });
    if(rsp.code != 200) {
      console.error('load strat tree failed', rsp);
      alerts.error(rsp.msg || 'load strat tree failed');
      return;
    }
    treeData.set(buildTree(rsp.data, true));
  }

  async function onTreeClick(event: { node: Node; collapsed: boolean }) {
    treeActive = event.node.id;
    if (!event.node.id.endsWith('/')) {
      const rsp = await getApi('/dev/bt_strat_text', { task_id: id, path: event.node.id });
      if(rsp.code != 200) {
        alerts.error(rsp.msg || 'load file content failed');
        return;
      }
      stratCodeContent = rsp.data;
      if(editor) {
        editor.setValue(event.node.name, stratCodeContent);
      }
    }
  }

  function changeTimeRange(start: number, end: number) {
    if(!task) return;
    const totalMS = task.stopAt - task.startAt;
    startMS = task.startAt + start * totalMS / 1000;
    endMS = task.startAt + end * totalMS / 1000;
    loadOrders();
  }

  function formatNumber(num: number, decimals = 2) {
    if(!num) return '0';
    if(decimals >= 6 && num > 1){
      decimals = 4;
    }
    return num.toFixed(decimals);
  }

  function onOrderAnalysis(order: InOutOrder) {
    clickOrder = order.id
    drawOrder = order;
    const exs = exsMap?.[order.symbol];
    if(!exs) {
      alerts.error(`symbol ${order.symbol} not found in ${Object.keys(exsMap || {}).length} items`);
      return;
    }
    const period = makePeriod(order.timeframe);
    const showStartMS = order.enter_at - period.secs * 1000 * 300;
    const showEndMS = order.exit_at + period.secs * 1000 * 100;
    const dayMs = 24 * 60 * 60 * 1000;
    pairOrders = [drawOrder];
    kcSave.update(c => {
      c.symbol = { market: exs.market, exchange: exs.exchange, ticker: order.symbol, name: order.symbol, shortName: order.symbol };
      c.period = period;
      c.dateStart = fmtDateStr(showStartMS, 'YYYYMMDD');
      c.dateEnd = fmtDateStr(showEndMS + dayMs, 'YYYYMMDD');
      return c;
    });
    kcCtx.update(c => {
      c.timeStart = showStartMS;
      c.timeEnd = showEndMS;
      c.fireOhlcv += 1;
      return c;
    });
  }

  async function drawChartOrders(startMS: number, endMS: number){
    if(!drawOrder || !kc)return
    const chart = kc.getChart();
    if (!chart) return;
    if (drawMultiple){
      // load orders for specified range
      const rsp = await getApi('/dev/bt_orders', {
        task_id: id,
        page: 1,
        page_size: 1000,
        symbol: drawOrder.symbol,
        start_ms: startMS,
        end_ms: endMS
      })
      if(rsp.code != 200) {
        alerts.error(rsp.msg || 'load orders failed');
        return;
      }
      pairOrders = rsp.orders;
    }

    // 绘制筛选后的订单
    pairOrders.forEach(td => {
      const color = td.short ? '#FF9600' : '#1677FF';
      const exitColor = td.short ? '#935EBD' : '#01C5C4';
      const inAction = `${td.short ? m.open_short() : m.open_long()}`;
      const outAction = `${td.short ? m.close_short() : m.close_long()}`;
      const isActive = td.id === drawOrder?.id;
      const enterMS = getFirstValid([td.enter?.update_at, td.enter?.create_at, td.enter_at]);

      const inText = `${inAction} ${td.enter_tag} ${td.leverage}x
${td.strategy}
${m.order()}: ${td.enter?.order_id}
${m.entry()}: ${fmtDateStr(enterMS)}
${m.price()}: ${td.enter?.average?.toFixed(5)}
${m.amount()}: ${td.enter?.amount.toFixed(6)}
${m.cost()}: ${td.quote_cost?.toFixed(2)}`;

      const points = [{
        timestamp: enterMS,
        value: getFirstValid([td.enter?.average, td.enter?.price, td.init_price])
      }];

      if (td.exit && td.exit.filled) {
        const exitMS = getFirstValid([td.exit?.update_at, td.exit?.create_at, td.exit_at]);
        const outText = `${outAction} ${td.exit_tag} ${td.leverage}x
${td.strategy}
${m.order()}: ${td.exit?.order_id}
${m.exit()}: ${fmtDateStr(exitMS)}
${m.price()}: ${td.exit?.average?.toFixed(5)}
${m.amount()}: ${td.exit?.amount?.toFixed(6)}
${m.profit()}: ${(td.profit_rate * 100).toFixed(1)}% ${td.profit.toFixed(5)}
${m.holding()}: ${fmtDuration((td.exit_at - td.enter_at) / 1000)}`;

        points.push({
          timestamp: exitMS,
          value: getFirstValid([td.exit?.average, td.exit?.price, td.init_price])
        });

        chart.createOverlay({
          name: 'trade',
          groupId: TRADE_GROUP,
          points,
          extendData: {
            line_color: color,
            in_color: color,
            in_text: inText,
            out_color: exitColor,
            out_text: outText,
            active: isActive,
          } as TradeInfo
        } as OverlayCreate)
        return;
      }
      if(!isActive)return;

      chart.createOverlay({
        name: 'note',
        groupId: TRADE_GROUP,
        points,
        extendData: {
          line_color: color,
          in_color: color,
          in_text: inText,
        } as TradeInfo
      } as OverlayCreate);
    });
  }

  function onOrderDetail(order: InOutOrder) {
    detailOrder = order;
    showOrderModal = true;
  }

  function formatPercent(num: number, decimals = 1) {
    if(!num) return '0%';
    return num.toFixed(decimals) + '%';
  }

  function formatDuration(ms: number) {
    const hours = Math.floor(ms / (3600 * 1000));
    const minutes = Math.floor((ms % (3600 * 1000)) / (60 * 1000));
    if(hours > 0) {
      return `${hours}h${minutes}m`;
    }
    return `${minutes}m`;
  }

  const klineLoad = derived(kcCtx, ($ctx) => $ctx.klineLoaded);
  klineLoad.subscribe(val => {
    if (!drawOrder || !kc) return;
    const chart = kc.getChart();
    if (!chart) return;

    chart.removeOverlay({ groupId: TRADE_GROUP });
    const klineDatas = chart.getDataList();
    if (klineDatas.length == 0)return;

    if(pairOrders.length == 0){
      console.log('no match orders to show')
      return;
    }
    const startMS = klineDatas[0].timestamp;
    const endMS = klineDatas[klineDatas.length-1].timestamp;
    drawChartOrders(startMS, endMS);

    // 计算缩放比例
    const tfMSecs = $kcSave.period.secs * 1000;
    const range = chart.getVisibleRange();
    const showStartMS = drawOrder.enter_at - tfMSecs * 20;
    const showEndMS = drawOrder.exit_at + tfMSecs * 20;
    const showBarNum = Math.ceil((showEndMS - showStartMS) / tfMSecs);
    const scale =  (range.to - range.from) / showBarNum;
    
    // 执行缩放和定位
    chart.scrollToTimestamp(showEndMS, 0);
    setTimeout(() => {
      chart.zoomAtTimestamp(scale, showEndMS, 0);
    }, 10);
  });

  interface TableColumn {
    title: string;
    field: keyof GroupData;
    format?: (value: any, row: GroupData) => string;
  }

  interface GroupData {
    title: string;
    orderNum: number;
    winCount: number;
    profitSum: number;
    costSum: number;
    durations: number[];
    sharpe: number;
    sortino: number;
    [key: string]: any;
  }

  function handlePageChange(newPage: number) {
    currentPage = newPage;
    loadOrders();
  }

  function handlePageSizeChange(newSize: number) {
    pageSize = newSize;
    loadOrders();
  }

  // 定义导航菜单项
  let navItems = $derived.by(() => {
    const items = [];
    
    if (task?.status == 3) {
      if (detail) {
        items.push(
          { id: 'overview', label: m.overview() },
          { id: 'assets', label: m.bt_assets() },
          { id: 'enters', label: m.bt_enters() }
        );
      }
      
      items.push({ id: 'config', label: m.configuration() });
      
      if (detail) {
        items.push(
          { id: 'orders', label: m.orders() },
          { id: 'analysis', label: m.bt_analysis() },
          { id: 'strat_code', label: m.bt_strat_code() }
        );
      }
    }
    
    items.push({ id: 'logs', label: m.bt_logs() });
    return items;
  });

  function setActiveTab(tab: string) {
    activeTab = tab;
    if(activeTab === 'orders') {
      loadOrders();
    } else if(activeTab === 'config') {
      loadConfig();
    } else if(activeTab === 'logs' && !logs) {
      loadLogs(true);
    } else if(activeTab === 'strat_code') {
      loadStratTree();
    } else if(activeTab === 'analysis') {
      loadOrders().then(() => {
        if(orders.length > 0) {
          onOrderAnalysis(orders[0]);
        }
      });
      setTimeout(() => {
        kc?.getChart()?.resize();
      }, 100)
    }
  }
</script>

<div class="px-4 py-6 flex-1 flex flex-col">
  <div class="flex justify-between items-center mb-6">
    <h1 class="text-2xl font-bold">{m.bt_report()}: {id}</h1>
    <div class="text-sm opacity-75">{btPath}</div>
  </div>

  <div class="flex gap-4 flex-1">
    <!-- 左侧导航 -->
    <div class="w-[13%] flex-shrink-0">
      <ul class="menu w-full bg-base-200 rounded-box">
        {#each navItems as item}
          <li>
            <button class:menu-active={activeTab === item.id} onclick={() => setActiveTab(item.id)}>
              {item.label}
            </button>
          </li>
        {/each}
      </ul>
      {#if activeTab == 'analysis'}
      <div class="flex flex-col gap-3 bg-base-200 mt-4 p-4 rounded-box">
        <fieldset class="fieldset">
          <input id="symbol" type="text" class="input w-full"
            bind:value={symbol} onchange={loadOrders} placeholder={m.symbol()}
          />
        </fieldset>
        <fieldset class="fieldset">
          <input id="strategy" type="text" class="input w-full"
            bind:value={strategy} onchange={loadOrders} placeholder={m.strategy()}
          />
        </fieldset>
        <fieldset class="fieldset">
          <input id="enterTag" type="text" class="input w-full"
            bind:value={enterTag} onchange={loadOrders} placeholder={m.enter_tag()}
          />
        </fieldset>
        <fieldset class="fieldset">
          <input id="exitTag" type="text" class="input w-full"
            bind:value={exitTag} onchange={loadOrders} placeholder={m.exit_tag()}
          />
        </fieldset>
        <button class="btn btn-sm btn-primary mt-2" onclick={loadOrders}>
          {m.load_orders()}
        </button>
      </div>
      {/if}
    </div>

    <!-- 右侧内容 -->
    <div class="flex-1 flex flex-col">
      <!-- K线图区域 -->
      <div class="h-[60vh] flex flex-col" class:hidden={activeTab !== 'analysis'}>
        <Chart class="border" bind:this={kc} ctx={kcCtx} save={kcSave} customLoad={true}/>
        <div class="flex justify-between">
          <RangeSlider class="mb-1 mt-3" data={odNums} bind:start={timeRange.start} bind:end={timeRange.end} change={changeTimeRange} />
          <label class="label cursor-pointer justify-end">
            <input type="checkbox" class="toggle toggle-primary" title={m.draw_multi_orders()} bind:checked={drawMultiple} 
              onchange={() => {
                drawOrder && onOrderAnalysis(drawOrder)
              }}/>
          </label>
        </div>
      </div>
      {#if activeTab === 'overview' && detail}
        {#if task?.status == 3}
        <!-- 重要统计信息 -->
        <div class="stats bg-base-100 stats-vertical lg:stats-horizontal shadow mb-6">
          {@render statCard(
            m.init_amount(),
            'text-success',
            formatNumber(task?.walletAmount || 0, 0),
            `${m.leverage()} ${task?.leverage}x${task?.stakeAmount ? ` · ${m.stake_amt()} ${formatNumber(task.stakeAmount, 0)}` : ''}`
          )}

          {@render statCard(
            m.profit_rate(),
            'text-success',
            formatPercent(detail.totProfitPct, 1),
            `${m.tot_profit()} ${formatNumber(detail.totProfit, 2)}`
          )}

          {@render statCard(
            m.win_rate(), '',
            formatPercent(detail.winRatePct),
            `${m.order_num()} ${detail.orderNum}`
          )}

          {@render statCard(
            m.max_drawdown(),
            'text-warning',
            formatPercent(detail.maxDrawDownPct),
            `${formatNumber(detail.maxDrawDownVal)} USD`
          )}
          
          {@render statCard(
            m.show_drawdown(),
            'text-warning',
            formatPercent(detail.showDrawDownPct),
            `${formatNumber(detail.showDrawDownVal)} USD`
          )}

          {@render statCard(
            m.sharpe_sortino(), '',
            formatNumber(detail.sharpeRatio, 2),
            formatNumber(detail.sortinoRatio, 2)
          )}
        </div>

        <!-- 次要统计信息 -->
        <div class="grid grid-cols-2 lg:grid-cols-3 gap-4 mb-6">
          {@render infoCard(m.bt_range() + ` (${curTZ()})`, `${fmtDateStr(detail.startMS)} ~ ${fmtDateStr(detail.endMS)}`)}
          {@render infoCard(
            `${m.total_invest()}/${m.final_balance()}/${m.total_withdraw()}`,
            `${task?.walletAmount ? formatNumber(task.walletAmount) : formatNumber(detail.totalInvest)} / ${formatNumber(detail.finBalance)} / ${formatNumber(detail.finWithdraw)}`
          )}
          {@render infoCard(m.tot_fee(), `${formatNumber(detail.totFee)} (${formatPercent(detail.totFee/detail.totProfit*100)})`)}
          {@render infoCard(m.strategy(), task?.strats || '-')}
          {@render infoCard(`${m.time_period()}/${m.bar_num()}`, `${task?.periods || '-'} / ${detail.barNum}`)}
          {@render infoCard(m.symbol() + '/' + m.max_open_orders(), `${task?.pairs ? showPairs(task.pairs) : '-'} / ${detail.maxOpenOrders}`)}
        </div>

        <!-- 分组统计 -->
        <div class="card bg-base-200 mb-6">
          <div class="card-body">
            <div role="tablist" class="tabs tabs-boxed mb-4">
              <button role="tab" 
                class="tab" 
                class:tab-active={activeGroupTab === 'pairs'}
                onclick={() => activeGroupTab = 'pairs'}>
                {m.stat_by_pairs()}
              </button>
              <button role="tab" 
                class="tab"
                class:tab-active={activeGroupTab === 'dates'}
                onclick={() => activeGroupTab = 'dates'}>
                {m.stat_by_dates()}
              </button>
              <button role="tab" 
                class="tab"
                class:tab-active={activeGroupTab === 'profits'}
                onclick={() => activeGroupTab = 'profits'}>
                {m.stat_by_profits()}
              </button>
              <button role="tab" 
                class="tab"
                class:tab-active={activeGroupTab === 'enters'}
                onclick={() => activeGroupTab = 'enters'}>
                {m.stat_by_enters()}
              </button>
              <button role="tab" 
                class="tab"
                class:tab-active={activeGroupTab === 'exits'}
                onclick={() => activeGroupTab = 'exits'}>
                {m.stat_by_exits()}
              </button>
            </div>

            <!-- 按品种统计 -->
            {#if activeGroupTab === 'pairs' && detail.pairGrps?.length > 0}
              {@render groupTable(detail.pairGrps as GroupData[], [
                { title: m.symbol(), field: 'title' },
                { title: m.order_num(), field: 'orderNum' },
                { title: m.win_rate(), field: 'winCount', format: (v, g) => formatPercent(v * 100 / (g.orderNum || 1)) },
                { title: m.tot_profit(), field: 'profitSum', format: v => formatNumber(v) },
                { title: m.profit_rate(), field: 'profitSum', format: (v, g) => formatPercent(v * 100 / g.costSum) },
                { title: m.avg_hold_time(), field: 'durations', format: (v: number[], g) => formatDuration(v.reduce((a, b) => a + b, 0) * 1000 / v.length) },
                { title: m.sharpe_ratio() + '/' + m.sortino_ratio(), field: 'sharpe', format: (v, g) => `${formatNumber(v, 2)} / ${formatNumber(g.sortino, 2)}` }
              ])}
            {/if}

            <!-- 按日期统计 -->
            {#if activeGroupTab === 'dates' && detail.dateGrps?.length > 0}
              <div class="overflow-x-auto">
                <table class="table">
                  <thead>
                    <tr>
                      <th>{m.date()}</th>
                      <th>{m.order_num()}</th>
                      <th>{m.win_rate()}</th>
                      <th>{m.tot_profit()}</th>
                      <th>{m.profit_rate()}</th>
                      <th>{m.avg_hold_time()}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {#each detail.dateGrps as group}
                      <tr>
                        <td>{group.title}</td>
                        <td>{group.orderNum || 0}</td>
                        <td>{formatPercent(group.winCount * 100 / (group.orderNum || 1))}</td>
                        <td>{formatNumber(group.profitSum)}</td>
                        <td>{formatPercent(group.profitSum * 100 / group.costSum)}</td>
                        <td>{formatDuration(group.durations.reduce((a: number, b: number) => a + b, 0) * 1000 / group.durations.length)}</td>
                      </tr>
                    {/each}
                  </tbody>
                </table>
              </div>
            {/if}

            <!-- 按收益区间统计 -->
            {#if activeGroupTab === 'profits' && detail.profitGrps?.length > 0}
              <div class="overflow-x-auto">
                <table class="table">
                  <thead>
                    <tr>
                      <th>{m.profit_range()}</th>
                      <th>{m.order_num()}</th>
                      <th>{m.win_rate()}</th>
                      <th>{m.tot_profit()}</th>
                      <th>{m.profit_rate()}</th>
                      <th>{m.avg_hold_time()}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {#each detail.profitGrps as group}
                      <tr>
                        <td>{group.title}</td>
                        <td>{group.orderNum || 0}</td>
                        <td>{formatPercent(group.winCount * 100 / (group.orderNum || 1))}</td>
                        <td>{formatNumber(group.profitSum)}</td>
                        <td>{formatPercent(group.profitSum * 100 / group.costSum)}</td>
                        <td>{formatDuration(group.durations.reduce((a: number, b: number) => a + b, 0) * 1000 / group.durations.length)}</td>
                      </tr>
                    {/each}
                  </tbody>
                </table>
              </div>
            {/if}

            <!-- 按入场信号统计 -->
            {#if activeGroupTab === 'enters' && detail.enterGrps?.length > 0}
              <div class="overflow-x-auto">
                <table class="table">
                  <thead>
                    <tr>
                      <th>{m.enter_tag()}</th>
                      <th>{m.order_num()}</th>
                      <th>{m.win_rate()}</th>
                      <th>{m.tot_profit()}</th>
                      <th>{m.profit_rate()}</th>
                      <th>{m.avg_hold_time()}</th>
                      <th>{m.sharpe_ratio()}/{m.sortino_ratio()}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {#each detail.enterGrps as group}
                      <tr>
                        <td>{group.title}</td>
                        <td>{group.orderNum || 0}</td>
                        <td>{formatPercent(group.winCount * 100 / (group.orderNum || 1))}</td>
                        <td>{formatNumber(group.profitSum)}</td>
                        <td>{formatPercent(group.profitSum * 100 / group.costSum)}</td>
                        <td>{formatDuration(group.durations.reduce((a: number, b: number) => a + b, 0) * 1000 / group.durations.length)}</td>
                        <td>{formatNumber(group.sharpe, 2)} / {formatNumber(group.sortino, 2)}</td>
                      </tr>
                    {/each}
                  </tbody>
                </table>
              </div>
            {/if}

            <!-- 按出场信号统计 -->
            {#if activeGroupTab === 'exits' && detail.exitGrps?.length > 0}
              <div class="overflow-x-auto">
                <table class="table">
                  <thead>
                    <tr>
                      <th>{m.exit_tag()}</th>
                      <th>{m.order_num()}</th>
                      <th>{m.win_rate()}</th>
                      <th>{m.tot_profit()}</th>
                      <th>{m.profit_rate()}</th>
                      <th>{m.avg_hold_time()}</th>
                      <th>{m.sharpe_ratio()}/{m.sortino_ratio()}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {#each detail.exitGrps as group}
                      <tr>
                        <td>{group.title}</td>
                        <td>{group.orderNum || 0}</td>
                        <td>{formatPercent(group.winCount * 100 / (group.orderNum || 1))}</td>
                        <td>{formatNumber(group.profitSum)}</td>
                        <td>{formatPercent(group.profitSum * 100 / group.costSum)}</td>
                        <td>{formatDuration(group.durations.reduce((a: number, b: number) => a + b, 0) * 1000 / group.durations.length)}</td>
                        <td>{formatNumber(group.sharpe, 2)} / {formatNumber(group.sortino, 2)}</td>
                      </tr>
                    {/each}
                  </tbody>
                </table>
              </div>
            {/if}

          </div>
        </div>
        {/if}

      {:else if activeTab === 'assets'}
        <iframe src={assetsUrl} class="w-full flex-1" title={m.bt_assets()} ></iframe>

      {:else if activeTab === 'enters'}
        <iframe src={entersUrl} class="w-full flex-1" title={m.bt_enters()} ></iframe>

      {:else if activeTab === 'config'}
        <CodeMirror bind:this={editor} {theme} readonly={true} class="flex-1" />

      {:else if activeTab === 'orders'}
        <div class="flex flex-col gap-4 flex-1">
          <!-- 过滤器 -->
          <div class="flex flex-wrap gap-4">
            <input type="text" placeholder={m.symbol()} class="input w-32"
              bind:value={symbol} onchange={loadOrders}
            />
            <input type="text" placeholder={m.strategy()} class="input w-32"
              bind:value={strategy} onchange={loadOrders}
            />
            <input type="text" placeholder={m.enter_tag()} class="input w-32"
              bind:value={enterTag} onchange={loadOrders}
            />
            <input type="text" placeholder={m.exit_tag()} class="input w-32"
              bind:value={exitTag} onchange={loadOrders}
            />
          </div>

          <!-- 订单表格 -->
          <div class="overflow-x-auto">
            <table class="table">
              <thead>
                <tr>
                  <th>{m.symbol()}</th>
                  <th>{m.direction()}</th>
                  <th>{m.leverage()}</th>
                  <th>{m.enter_at()}({curTZ()})</th>
                  <th>{m.enter_tag()}</th>
                  <th>{m.enter_price()}</th>
                  <th>{m.enter_amount()}</th>
                  <th>{m.exit_at()}({curTZ()})</th>
                  <th>{m.exit_tag()}</th>
                  <th>{m.exit_price()}</th>
                  <th>{m.exit_amount()}</th>
                  <th>{m.profit()}</th>
                </tr>
              </thead>
              <tbody>
                {#each orders as order}
                  <tr class="hover:bg-base-300 cursor-pointer" onclick={() => onOrderDetail(order)}>
                    <td>{order.symbol}</td>
                    <td>{order.short ? m.short() : m.long()}</td>
                    <td>{order.leverage}x</td>
                    <td>{fmtDateStr(order.enter_at)}</td>
                    <td>{order.enter_tag}</td>
                    <td>{formatNumber(order.enter?.average||order.enter?.price || 0, 8)}</td>
                    <td>{formatNumber(order.enter?.filled ||order.enter?.amount || 0, 8)}</td>
                    <td>{fmtDateStr(order.exit_at)}</td>
                    <td>{order.exit_tag}</td>
                    <td>{formatNumber(order.exit?.average||order.exit?.price || 0, 8)}</td>
                    <td>{formatNumber(order.exit?.filled ||order.exit?.amount || 0, 8)}</td>
                    <td>{formatNumber(order.profit)}</td>
                  </tr>
                {/each}
              </tbody>
            </table>
          </div>

          {@render pagination(total, pageSize, currentPage, handlePageChange, handlePageSizeChange)}
        </div>

      {:else if activeTab === 'strat_code'}
        <div class="flex flex-1">
          <div class="w-1/5 border-r border-base-300 flex flex-col p-4">
            <TreeView tree={$treeData} active={treeActive} click={onTreeClick} treeClass="gap-1" />
          </div>
          
          <div class="flex-1">
            <CodeMirror bind:this={editor} {theme} readonly={true} class="h-full" />
          </div>
        </div>

      {:else if activeTab === 'analysis'}
        <!-- 订单列表区域 -->
        <div class="min-h-[20vh]">
          <div class="p-2 flex flex-wrap justify-between">
            {#each orders as order}
              {@render orderCard(
                order,
                clickOrder === order.id,
                () => onOrderAnalysis(order),
                () => onOrderDetail(order)
              )}
            {/each}
            <!-- 添加占位元素 -->
            {#each Array(10) as _}
              <div class="w-[15em] mr-2 h-0 invisible"></div>
            {/each}
          </div>
          <!-- 分页器 -->
          {@render pagination(total, pageSize, currentPage, handlePageChange, handlePageSizeChange)}
        </div>

      {:else if activeTab === 'logs'}
        <div class="flex flex-col gap-4 flex-1">
          <div class="bg-base-200 rounded-box p-4 font-mono text-sm whitespace-pre-wrap flex-1 overflow-y-auto">
            <a class="link link-primary link-hover" onclick={() => loadLogs(false)}>{loadingLogs ? m.loading() : m.load_more()}</a>
            <br/>
            {logs}
            {#if task?.status == 2}
              <a class="link link-primary link-hover" onclick={() => loadLogs(true)}>{loadingLogs ? m.refreshing() : m.refresh()}</a>
            {/if}
          </div>
        </div>
      {/if}
    </div>
  </div>

  <!-- 订单详情弹窗 -->
  <OrderDetail bind:show={showOrderModal} order={detailOrder} editable={false} />
</div>

{#snippet statCard(title: string, claName: string, value: string | number, desc: string)}
  <div class="stat">
    <div class="stat-title">{title}</div>
    <div class="stat-value {claName}">{value}</div>
    <div class="stat-desc">{desc}</div>
  </div>
{/snippet}

{#snippet infoCard(title: string, content: string)}
  <div class="card bg-base-200">
    <div class="card-body">
      <h3 class="card-title text-sm">{title}</h3>
      <p class="break-all">{content}</p>
    </div>
  </div>
{/snippet}

{#snippet groupTable(groups: GroupData[], columns: TableColumn[])}
  <div class="overflow-x-auto">
    <table class="table">
      <thead>
        <tr>
          {#each columns as col}
            <th>{col.title}</th>
          {/each}
        </tr>
      </thead>
      <tbody>
        {#each groups as group}
          <tr>
            {#each columns as col}
              <td>{col.format ? col.format(group[col.field], group) : group[col.field]}</td>
            {/each}
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
{/snippet}