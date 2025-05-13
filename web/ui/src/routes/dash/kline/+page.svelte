<script lang="ts">
  import { onMount } from 'svelte';
  import { page } from '$app/state';
  import Chart from '$lib/kline/Chart.svelte';
  import { getAccApi } from '$lib/netio';
  import { alerts } from '$lib/stores/alerts';
  import type { OverlayCreate } from 'klinecharts';
  import { fmtDuration, fmtDateStr } from '$lib/dateutil';
  import * as m from '$lib/paraglide/messages';
  import { derived } from 'svelte/store';
  import { ChartCtx, ChartSave } from '$lib/kline/chart';
  import { persisted } from 'svelte-persisted-store';
  import { writable } from 'svelte/store';
  import RangeSlider from '$lib/dev/RangeSlider.svelte';
  import type { InOutOrder, OdGroup } from '$lib/order/types';
  import type { TradeInfo } from '$lib/kline/types';
  import { pagination, orderCard } from '$lib/Snippets.svelte';
  import {makePeriod} from "$lib/kline/coms";
  import type {ExSymbol} from "$lib/dev/common";
  import {OrderDetail} from "$lib/order";
  import {getFirstValid} from "$lib/common";
  import _ from "lodash"

  // 状态变量
  let search = $state({
    strategy: '',
    enterTag: '',
    exitTag: '',
    symbol: '',
    startTime: '',
    endTime: '',
    groupBy: '',
  });

  let job = $state({
    strategy: '',
    enterTag: '',
    exitTag: '',
    symbol: '',
    // 下面条件跟search无关
    startMS: 0,
    endMS: 0,
    afterId: 1,
    page: 0,
    pageSize: 10
  })

  let total = $state(0);
  let tradeList = $state<InOutOrder[]>([]);
  let pairOrders = $state<InOutOrder[]>([]);
  let detailOrder = $state<InOutOrder | null>(null);
  let clickOrder = $state<InOutOrder | null>(null);
  let drawOrder = $state<InOutOrder | null>(null);
  let drawMultiple = $state(true);
  let gpStart = $state(0);
  let gpEnd = $state(0);
  let timeRange = $state({ start: 0, end: 300 });
  let odNums = $state<number[]>([]);
  let exsMap = $state<Record<string, ExSymbol> | null>(null);
  let showOrderModal = $state(false);

  // 分组统计相关
  let groupStats = $state<OdGroup[]>([]);

  const kcCtx = writable<ChartCtx>(new ChartCtx());
  let saveRaw = new ChartSave();
  saveRaw.key = 'chart';
  const kcSave = persisted(saveRaw.key, saveRaw);
  const TRADE_GROUP = 'ban_trades';
  let kc: Chart;

  onMount(() => {
    let pair = page.url.searchParams.get('pair');
    if(pair){
      search.symbol = pair;
    }
    loadGroupStats();
    loadExsMap();
    const initDone = derived(kcCtx, ($c) => $c.initDone)
    initDone.subscribe(() => {
      if(pair){
        kcSave.update((s) => {
          s.symbol.ticker = pair
          s.symbol.shortName = pair
          return s
        })
      }
    })
  });

  async function loadExsMap(){
    const rsp = await getAccApi('/exs_map');
    if(rsp.code == 200){
      exsMap = rsp.data
    }
  }

  async function loadOrders() {
    const rsp = await getAccApi('/orders', { 
      symbols: job.symbol,
      source: 'bot',
      strategy: job.strategy,
      enterTag: job.enterTag,
      exitTag: job.exitTag,
      startMs: job.startMS,
      stopMs: job.endMS,
      limit: job.pageSize,
      afterId: job.afterId
    });
    if (rsp.code === 200) {
      tradeList = rsp.data ?? [];
      total = rsp.total ?? 0;
    } else {
      alerts.error(rsp.msg ?? m.load_orders_failed());
      return;
    }
    if (tradeList.length > 0 && !clickOrder) {
      onOrderSelect(tradeList[0]);
    }
  }

  async function loadGroupStats() {
    if(!search.symbol){
      search.groupBy = 'symbol'
    }else if(!search.strategy){
      search.groupBy = 'strategy';
    }else if(!search.enterTag){
      search.groupBy = 'enterTag';
    }else if(!search.exitTag){
      search.groupBy = 'exitTag';
    }
    const rsp = await getAccApi('/group_sta', search);

    if (rsp.code === 200) {
      groupStats = rsp.data ?? [];
    }
  }

  function clickOdGroup(stat: OdGroup){
    odNums = stat.nums;
    gpStart = stat.minTime;
    gpEnd = stat.maxTime;
    job.strategy = search.strategy;
    job.symbol = search.symbol;
    job.enterTag = search.enterTag;
    job.exitTag = search.exitTag;
    job.startMS = stat.minTime;
    job.endMS = stat.maxTime;
    job.afterId = 0;
    job.page = 1;
    loadOrders();
  }

  function changeTimeRange(start: number, end: number) {
    let rangeMS = gpEnd - gpStart
    job.startMS = Math.round(gpStart + start/1000*rangeMS)
    job.endMS = Math.round(gpStart + end/1000*rangeMS)
    job.afterId = 0;
    job.page = 1;
    loadOrders();
  }

  function handlePageChange(newPage: number) {
    if(newPage == job.page)return
    if (tradeList.length > 0){
      if(newPage > job.page){
        job.afterId = tradeList[tradeList.length-1].id
      }else{
        job.afterId = -tradeList[0].id
      }
    }if(newPage < job.page){
      // 当前为空，返回上一页
      job.afterId = -Math.abs(job.afterId)
    }
    job.page = newPage;
    loadOrders();
  }

  function handlePageSizeChange(newSize: number) {
    job.pageSize = newSize;
    job.afterId = 0;
    loadOrders();
  }

  function onOrderSelect(order: InOutOrder) {
    clickOrder = order;
    drawOrder = order;
    const exs = exsMap?.[order.symbol];
    if (!exs) {
      alerts.error(`symbol ${order.symbol} not found in ${Object.keys(exsMap || {}).length} items`);
      return;
    }
    const period = makePeriod(order.timeframe);
    const tfMSecs = period.secs * 1000;
    const showStartMS = order.enter_at - tfMSecs * 300;
    const showEndMS = order.exit_at + tfMSecs * 100;
    const dayMs = 24 * 60 * 60 * 1000;

    function fireLoad() {
      if (!exs) return;
      kcSave.update(c => {
        c.symbol = {
          market: exs.market,
          exchange: exs.exchange,
          ticker: order.symbol,
          name: order.symbol,
          shortName: order.symbol
        };
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

    pairOrders = [drawOrder];
    if (!drawMultiple) {
      fireLoad();
      return;
    }
    getAccApi('/orders', {
      symbols: order.symbol,
      source: 'bot',
      strategy: job.strategy,
      enterTag: job.enterTag,
      exitTag: job.exitTag,
      startMs: showStartMS,
      stopMs: showEndMS,
      limit: 100,
    }).then(rsp => {
      if (rsp.code === 200) {
        pairOrders = rsp.data ?? [];
        fireLoad();
      }else{
        alerts.error(rsp.msg || 'load orders failed');
      }
    });
  }

  let paintOrders = _.debounce(val => {
    if (!drawOrder || !kc) return;
    if(pairOrders.length === 0){
      console.log('no match orders to show');
      return;
    }
    const chartObj = kc.getChart();
    if (!chartObj) return;

    chartObj.removeOverlay({ groupId: TRADE_GROUP });
    const klineDatas = chartObj.getDataList();
    if (klineDatas.length == 0)return;

    // 计算缩放比例
    const tfMSecs = $kcSave.period.secs * 1000;
    const range = chartObj.getVisibleRange();
    const showStartMS = drawOrder.enter_at - tfMSecs * 20;
    const showEndMS = drawOrder.exit_at + tfMSecs * 20;
    const showBarNum = Math.ceil((showEndMS - showStartMS) / tfMSecs);
    const scale =  (range.to - range.from) / showBarNum;

    chartObj.scrollToTimestamp(showEndMS, 0);
    setTimeout(() => {
      chartObj.zoomAtTimestamp(scale, showEndMS, 0);
    }, 10);

    // 绘制订单标记
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
${m.amount()}: ${td.enter?.amount?.toFixed(6)}
${m.cost()}: ${td.quote_cost?.toFixed(2)}`;

      const points = [{
        timestamp: enterMS,
        value: getFirstValid([td.enter?.average, td.enter?.price, td.init_price])
      }];

      if (td.exit && (td.exit?.create_at || td.exit_at)) {
        const exitMS = getFirstValid([td.exit?.update_at, td.exit?.create_at, td.exit_at]);
        const outText = `${outAction} ${td.exit_tag} ${td.leverage}x
${td.strategy}
${m.order()}: ${td.exit?.order_id}
${m.exit()}: ${fmtDateStr(exitMS)}
${m.price()}: ${td.exit?.average?.toFixed(5)}
${m.amount()}: ${td.exit?.amount?.toFixed(6)}
${m.profit()}: ${(td.profit_rate * 100).toFixed(1)}% ${td.profit.toFixed(5)}
${m.holding()}: ${fmtDuration(td.exit_at - enterMS)}`;

        points.push({
          timestamp: exitMS,
          value: getFirstValid([td.exit?.average, td.exit?.price, td.init_price])
        });

        chartObj.createOverlay({
          name: 'trade',
          groupId: TRADE_GROUP,
          points,
          extendData: {
            line_color: color,
            in_color: color,
            in_text: inText,
            out_color: exitColor,
            out_text: outText,
            active: isActive
          } as TradeInfo
        } as OverlayCreate);
        return;
      }
      if(!isActive)return;

      chartObj.createOverlay({
        name: 'note',
        groupId: TRADE_GROUP,
        points,
        extendData: {
          line_color: color,
          in_color: color,
          in_text: inText,
          active: td.id === drawOrder?.id
        } as TradeInfo
      } as OverlayCreate);
    });
  }, 200)

  const klineLoad = derived(kcCtx, ($ctx) => $ctx.klineLoaded);
  klineLoad.subscribe(val => {
    paintOrders(val)
  })

  function onOrderDetail(order: InOutOrder) {
    detailOrder = order;
    showOrderModal = true;
  }

  function formatPercent(num: number) {
    return (num * 100).toFixed(1) + '%';
  }

</script>

<div class="flex gap-2 h-[calc(100vh-4rem)]">
  <!-- 左侧筛选和分组 -->
  <div class="w-[200px] flex flex-col gap-2 p-2 h-full">
    <!-- 筛选条件 -->
    <div class="card bg-base-100 shrink-0">
      <div class="card-body p-0">
        <div class="flex flex-col gap-1">
          <fieldset class="fieldset">
            <input type="text" class="input input-sm" placeholder={m.symbol()} bind:value={search.symbol}/>
          </fieldset>
          <fieldset class="fieldset">
            <input type="text" class="input input-sm" placeholder={m.strategy()} bind:value={search.strategy}/>
          </fieldset>
          <fieldset class="fieldset">
            <input type="text" class="input input-sm" placeholder={m.enter_tag()} bind:value={search.enterTag}/>
          </fieldset>
          <fieldset class="fieldset">
            <input type="text" class="input input-sm" placeholder={m.exit_tag()} bind:value={search.exitTag}/>
          </fieldset>
          <fieldset class="fieldset">
            <input type="text" class="input input-sm" placeholder={m.start_time()} bind:value={search.startTime}/>
          </fieldset>
          <fieldset class="fieldset">
            <input type="text" class="input input-sm" placeholder={m.end_time()} bind:value={search.endTime}/>
          </fieldset>
          <button class="btn btn-sm btn-primary mt-2" onclick={loadGroupStats}>
            {m.search()}
          </button>
        </div>
      </div>
    </div>

    <!-- 分组统计 -->
    <div class="bg-base-100 flex-1 overflow-hidden flex flex-col">
      <div class="overflow-y-auto flex-1">
        {#each groupStats as stat}
          <div class="card bg-base-200 mb-2 hover:bg-base-300 cursor-pointer"
               onclick={() => clickOdGroup(stat)}>
            <div class="card-body p-3">
              <div class="text-sm">{stat.key}</div>
              <div class="text-sm opacity-75 flex justify-between">
                <span title="{m.order_num()}">{stat.closeNum}</span>
                <span title="{m.win_rate()}">{formatPercent(stat.winNum / stat.closeNum)}</span>
                <span title="{m.tot_profit()}">{stat.profitSum.toFixed(2)}</span>
              </div>
            </div>
          </div>
        {/each}
      </div>
    </div>
  </div>

  <!-- 右侧K线和订单列表 -->
  <div class="flex-1 flex flex-col gap-2 h-full">
    <!-- K线图区域 -->
    <div class="shrink-0">
      <Chart bind:this={kc} ctx={kcCtx} save={kcSave} customLoad={true} />
      <div class="flex justify-between items-center mt-2">
        <RangeSlider class="flex-1 mr-4" data={odNums} bind:start={timeRange.start} bind:end={timeRange.end}
          change={changeTimeRange}/>
        <label class="label cursor-pointer gap-2">
          <input type="checkbox" class="toggle toggle-primary" bind:checked={drawMultiple}
            title={m.draw_multi_orders()} onchange={() => clickOrder && onOrderSelect(clickOrder)}
          />
        </label>
      </div>
    </div>

    <!-- 订单列表 -->
    <div class="flex-1 bg-base-100 overflow-hidden flex flex-col">
      <div class="overflow-y-auto flex-1">
        <div class="flex flex-wrap justify-between">
          {#each tradeList as order}
            {@render orderCard(
              order,
              clickOrder?.id === order.id,
              () => onOrderSelect(order),
              () => onOrderDetail(order)
            )}
          {/each}
          <!-- 添加占位元素 -->
          {#each Array(10) as _}
            <div class="w-[15em] mr-2 h-0 invisible"></div>
          {/each}
        </div>
        <!-- 分页器 -->
        {@render pagination(total, job.pageSize, job.page, handlePageChange, handlePageSizeChange)}
      </div>
    </div>
  </div>

  <!-- 订单详情弹窗 -->
  <OrderDetail bind:show={showOrderModal} order={detailOrder} editable={false} />
</div>
