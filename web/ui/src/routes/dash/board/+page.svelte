<script lang="ts">
  import { onMount } from 'svelte';
  import * as m from '$lib/paraglide/messages.js';
  import { getAccApi, postAccApi } from '$lib/netio';
  import { alerts } from '$lib/stores/alerts';
  import { acc, loadAccounts } from '$lib/dash/store';
  import { fmtDateStr, fmtDateStrTZ, getUTCStamp } from '$lib/dateutil';
  import Icon from '$lib/Icon.svelte';

  interface BalanceItem{
    symbol: string;
    total: number;
    upol: number;
    free: number;
    used: number;
    total_fiat: number;
  }

  interface BotStatus {
    cpuPct: number;
    ramPct: number;
    lastProcess: number;
    allowTradeAt: number;
    doneProfitPctMean: number;
    doneProfitMean: number;
    doneProfitPctSum: number;
    doneProfitSum: number;
    allProfitPctMean: number;
    allProfitMean: number;
    allProfitPctSum: number;
    allProfitSum: number;
    orderNum: number;
    doneOrderNum: number;
    firstOdTs: number;
    lastOdTs: number;
    avgDuration: number;
    bestPair: string;
    bestProfitPct: number;
    winNum: number;
    lossNum: number;
    profitFactor: number;
    winRate: number;
    expectancy: number;
    expectancyRatio: number;
    maxDrawdownPct: number;
    maxDrawdownVal: number;
    totalCost: number;
    botStartMs: number;
    balanceTotal: number;
    balanceItems: BalanceItem[];
    runTfs: string[];
    exchange: string;
    market: string;
    pairs: string[];
    env: string;
  }

  let data = $state<BotStatus>({
    cpuPct: 0,
    ramPct: 0,
    lastProcess: 0,
    allowTradeAt: 0,
    doneProfitPctMean: 0,
    doneProfitMean: 0,
    doneProfitPctSum: 0,
    doneProfitSum: 0,
    allProfitPctMean: 0,
    allProfitMean: 0,
    allProfitPctSum: 0,
    allProfitSum: 0,
    orderNum: 0,
    doneOrderNum: 0,
    firstOdTs: 0,
    lastOdTs: 0,
    avgDuration: 0,
    bestPair: '',
    bestProfitPct: 0,
    winNum: 0,
    lossNum: 0,
    profitFactor: 0,
    winRate: 0,
    expectancy: 0,
    expectancyRatio: 0,
    maxDrawdownPct: 0,
    maxDrawdownVal: 0,
    totalCost: 0,
    botStartMs: 0,
    balanceTotal: 0,
    balanceItems: [],
    runTfs: [],
    exchange: '',
    market: '',
    pairs: [],
    env: ''
  });

  let delayHours = $state(3);
  let entryStatus = $state('');

  async function saveAllowEntry() {
    const secs = delayHours * 3600;
    const rsp = await postAccApi('/delay_entry', {secs});
    
    if(rsp.code === 200 && rsp.allowTradeAt) {
      data.allowTradeAt = rsp.allowTradeAt;
      alerts.success(m.save_success());
      return;
    }
    alerts.error(m.save_failed({msg: rsp.msg ?? ''}));
  }

  async function refreshWallet(){
    const rsp = await postAccApi('/refresh_wallet', {});
    if(rsp.code === 200) {
      data.balanceTotal = rsp.total ?? 0;
      data.balanceItems = rsp.items ?? [];
    }else{
      alerts.error(rsp.msg ?? '');
    }
  }

  onMount(async () => {
    await loadAccounts(false)

    // 系统信息：CPU内存，上次处理时间
    let rsp = await getAccApi('/bot_info');
    if(rsp.code === 200) {
      Object.assign(data, rsp);
    }
    
    // 整体详细概况
    rsp = await getAccApi('/statistics');
    if(rsp.code === 200) {
      Object.assign(data, rsp);
    }
    
    // 显示余额情况
    rsp = await getAccApi('/balance');
    if(rsp.code === 200) {
      data.balanceTotal = rsp.total ?? 0;
      data.balanceItems = rsp.items ?? [];
    }

    const curMs = getUTCStamp();
    if(data.allowTradeAt <= curMs) {
      entryStatus = m.allow_entry();
    }else{
      const dateStr = fmtDateStr(data.allowTradeAt, 'MM-DD HH:mm');
      entryStatus = m.no_entry_until({date: dateStr});
    }
  });
</script>

<div class="space-y-6 p-4">
  <!-- Stats Row -->
  <div class="stats shadow-md bg-base-100 rounded-lg w-full border border-base-200">
    <div class="stat px-4">
      <div class="stat-title text-sm text-base-content/70">{m.watch_pairs()}</div>
      <div class="stat-value text-2xl mt-1.5">{data.pairs.length}</div>
    </div>
    
    <div class="stat px-4">
      <div class="stat-title text-sm text-base-content/70">{m.open_close_num()}</div>
      <div class="stat-value text-2xl mt-1.5">{data.orderNum - data.doneOrderNum} / {data.doneOrderNum}</div>
    </div>
    
    <div class="stat px-4">
      <div class="stat-title text-sm text-base-content/70">{m.win_loss_rate()}</div>
      <div class="stat-value text-2xl mt-1.5">
        <span class="text-success">{data.winNum}</span> / <span class="text-error">{data.lossNum}</span> / <span class="text-primary">{(data.winRate * 100).toFixed(0)}%</span>
      </div>
    </div>
    
    <div class="stat px-4">
      <div class="stat-title text-sm text-base-content/70">{m.profit_factor()}</div>
      <div class="stat-value text-2xl mt-1.5">{data.profitFactor.toFixed(2)}</div>
    </div>
  </div>

  <!-- System Info -->
  <div class="card bg-base-100 shadow-md hover:shadow-lg transition-shadow rounded-lg border border-base-200">
    <div class="card-body p-4">
      <h2 class="card-title text-lg font-medium mb-3">{m.system_info()}</h2>
      <div class="grid grid-cols-1 md:grid-cols-3 gap-4">
        <div class="flex items-center space-x-2">
          <span class="text-sm text-base-content/70">CPU:</span>
          <span class="text-sm">{data.cpuPct.toFixed(1)}%</span>
        </div>
        <div class="flex items-center space-x-2">
          <span class="text-sm text-base-content/70">{m.market()}:</span>
          <span class="text-sm font-mono">{data.exchange}.{data.market}</span>
        </div>
        <div class="flex items-center space-x-2">
          <span class="text-sm text-base-content/70">{m.last_active()}:</span>
          <span class="text-sm">{fmtDateStrTZ(data.lastProcess)}</span>
        </div>
        <div class="flex items-center space-x-2">
          <span class="text-sm text-base-content/70">RAM:</span>
          <span class="text-sm">{data.ramPct.toFixed(1)}%</span>
        </div>
        <div class="flex items-center space-x-2">
          <span class="text-sm text-base-content/70">{m.timeframes()}:</span>
          <span class="text-sm font-mono">{data.runTfs.join(', ')}</span>
        </div>
        <div class="flex items-center space-x-2">
          <span class="text-sm text-base-content/70">{m.start_time()}:</span>
          <span class="text-sm">{fmtDateStrTZ(data.botStartMs)}</span>
        </div>
      </div>
    </div>
  </div>

  <!-- Trading Stats -->
  <div class="card bg-base-100 shadow-md hover:shadow-lg transition-shadow rounded-lg border border-base-200">
    <div class="card-body p-4">
      <h2 class="card-title text-lg font-medium mb-3">{m.trading_stats()}</h2>
      <div class="mb-4 flex flex-wrap items-center gap-3">
        <span class="text-sm text-base-content/70">{m.run_env()}:</span>
        <span class="text-sm mr-8">{data.env}</span>
        <span class="text-sm text-base-content/70">{m.entry_status()}:</span>
        <span class="text-sm mr-4">{entryStatus}</span>
        <div class="join shadow-sm">
          <span class="join-item px-3 py-2 flex items-center bg-base-200/50 text-sm text-base-content/70">
            {m.delay_hours()}
          </span>
          <input type="number" class="input join-item w-20 focus:outline-none text-sm"
                 bind:value={delayHours}
                 min="0" max="9999" step="0.1"/>
          <button class="btn join-item hover:bg-primary hover:text-primary-content transition-colors" onclick={saveAllowEntry}>
            {m.save()}
          </button>
        </div>
      </div>
      <div class="overflow-x-auto">
        <table class="table table-sm">
          <tbody>
            <!-- All Orders -->
            <tr>
              <th colspan="4" class="text-center bg-base-200/50 font-medium text-sm">{m.all_orders()}</th>
            </tr>
            <tr>
              <td class="text-sm text-base-content/70">{m.avg_profit_rate()}</td>
              <td class="text-sm font-mono">{data.allProfitPctMean.toFixed(1)}%</td>
              <td class="text-sm text-base-content/70">{m.avg_profit()}</td>
              <td class="text-sm font-mono">{data.allProfitMean.toFixed(5)}</td>
            </tr>
            <tr>
              <td class="text-sm text-base-content/70">{m.total_profit_rate()}</td>
              <td class="text-sm font-mono">{data.allProfitPctSum.toFixed(1)}%</td>
              <td class="text-sm text-base-content/70">{m.total_profit()}</td>
              <td class="text-sm font-mono">{data.allProfitSum.toFixed(5)}</td>
            </tr>
            
            <!-- Closed Orders -->
            <tr>
              <th colspan="4" class="text-center bg-base-200/50 font-medium text-sm">{m.closed_orders()}</th>
            </tr>
            <tr>
              <td class="text-sm text-base-content/70">{m.avg_profit_rate()}</td>
              <td class="text-sm font-mono">{data.doneProfitPctMean.toFixed(1)}%</td>
              <td class="text-sm text-base-content/70">{m.avg_profit()}</td>
              <td class="text-sm font-mono">{data.doneProfitMean.toFixed(5)}</td>
            </tr>
            <tr>
              <td class="text-sm text-base-content/70">{m.total_profit_rate()}</td>
              <td class="text-sm font-mono">{data.doneProfitPctSum.toFixed(1)}%</td>
              <td class="text-sm text-base-content/70">{m.total_profit()}</td>
              <td class="text-sm font-mono">{data.doneProfitSum.toFixed(5)}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>
  </div>

  <!-- Balance Info -->
  <div class="card bg-base-100 shadow-md hover:shadow-lg transition-shadow rounded-lg border border-base-200">
    <div class="card-body p-4">
      <h2 class="card-title text-lg font-medium mb-3">
        {m.wallet_balance()}
        {#if data.env !== 'dry_run'}
          <Icon name="refresh" class="size-6 cursor-pointer" onclick={refreshWallet}/>
        {/if}
      </h2>
      {#if data.balanceItems.length > 1}
        <div class="text-base font-medium mb-3 text-primary">{m.total()}: {data.balanceTotal.toFixed(4)}</div>
      {/if}
      <div class="flex flex-wrap gap-2">
        {#each data.balanceItems as item}
          <div class="badge badge-md gap-1.5 py-2.5 px-3 bg-base-200/70 text-base-content border-none">
            <span class="font-medium text-sm">{item.symbol}</span>
            <span class="text-primary text-sm font-mono" title={m.wallet_total()}>{item.total.toFixed(6)}</span>
            <span class="text-info text-sm font-mono" title={m.wallet_free()}>/ {item.free.toFixed(6)}</span>
            <span class="text-info text-sm font-mono" title={m.wallet_upol()}>/ {item.upol.toFixed(6)}</span>
          </div>
        {/each}
      </div>
    </div>
  </div>
</div> 