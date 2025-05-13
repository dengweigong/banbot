<script lang="ts">
  import { onMount } from 'svelte';
  import * as m from '$lib/paraglide/messages';
  import { getAccApi } from '$lib/netio';
  import Modal from '$lib/kline/Modal.svelte';
  import {curTZ, fmtDateStrTZ} from '$lib/dateutil';

  interface StgyVer {
    name: string;
    version: number;
  }

  interface PairStgyTf {
    pair: string;
    strategy: string;
    tf: string;
    price: number;
    odNum: number;
    lastBarMS: number;
    args: Array<{title: string, value: any}>;
  }

  let selectedStrategy = $state('all');
  let jobs = $state<PairStgyTf[]>([]);
  let stratList = $state<StgyVer[]>([]);
  let showJobEdit = $state(false);
  let showCode = $state(false);
  let activeStgy = $state('');
  let stgyContent = $state('');
  
  let job = $state<PairStgyTf>({
    pair: '',
    strategy: '',
    tf: '',
    price: 0,
    odNum: 0,
    lastBarMS: 0,
    args: []
  });

  // 获取指定策略下的所有交易对信息
  function getPairsByStrategy(strategy: string) {
    if (strategy === 'all') {
      // 按交易对分组，合并信息
      const pairGroups = jobs.reduce((acc, job) => {
        if (!acc[job.pair]) {
          acc[job.pair] = {
            pair: job.pair,
            tfs: new Set([job.tf]),
            price: job.price,
            totalOdNum: job.odNum,
            lastBarMS: job.lastBarMS,
            strategies: new Set([job.strategy])
          };
        } else {
          acc[job.pair].tfs.add(job.tf);
          acc[job.pair].totalOdNum += job.odNum;
          acc[job.pair].lastBarMS = Math.max(job.lastBarMS, acc[job.pair].lastBarMS);
          acc[job.pair].strategies.add(job.strategy);
        }
        return acc;
      }, {} as Record<string, any>);

      return Object.values(pairGroups)
        .map(group => ({
          ...group,
          tfs: Array.from(group.tfs).join(', '),
          strategies: Array.from(group.strategies),
          strategyCount: group.strategies.size
        }))
        .sort((a, b) => a.pair.localeCompare(b.pair));
    } else {
      // 返回特定策略的jobs，按pair排序
      return jobs
        .filter(job => job.strategy === strategy)
        .sort((a, b) => a.pair.localeCompare(b.pair));
    }
  }

  async function loadData() {
    const rsp = await getAccApi('/stg_jobs');
    if(rsp.code === 200) {
      jobs = rsp.jobs ?? [];
      const strats: Record<string, number> = rsp.strats ?? {};
      stratList = Object.entries(strats).map(([name, version]) => ({
        name,
        version
      }));
    }
  }

  function editJobArgs(row: PairStgyTf) {
    job = {...row};
    showJobEdit = true;
  }

  function showJobArgs(row: PairStgyTf) {
    if(!Array.isArray(row.args)) return '';
    return row.args
      .filter(item => item.value)
      .map(item => item.title)
      .join(', ');
  }

  onMount(() => {
    loadData();
  });
</script>

<Modal title={activeStgy} bind:show={showCode}>
  <pre class="overflow-auto max-h-[63vh] h-[800px] p-3 bg-base-200/70 rounded-lg text-sm font-mono break-all">{stgyContent}</pre>
</Modal>

<div class="bg-base-100 p-4 min-h-[600px] border border-base-200 flex">
  <!-- 左侧策略列表 -->
  <div class="w-1/5 pr-4 border-r border-base-200">
    <div class="space-y-1">
      <button 
        class="w-full text-left px-3 py-2 rounded-lg text-sm transition-colors {selectedStrategy === 'all' ? 'bg-primary/90 text-primary-content' : 'hover:bg-base-200'}"
        onclick={() => selectedStrategy = 'all'}
      >{m.all()}</button>
      {#each stratList as stgy}
        <button 
          class="w-full text-left px-3 py-2 rounded-lg text-sm transition-colors {selectedStrategy === stgy.name ? 'bg-primary/90 text-primary-content' : 'hover:bg-base-200'}"
          onclick={() => selectedStrategy = stgy.name}
        >
          {stgy.name}
        </button>
      {/each}
    </div>
  </div>

  <!-- 右侧交易对列表 -->
  <div class="flex-1">
    <div class="overflow-x-auto">
      <table class="table table-sm">
        <thead>
          <tr>
            <th class="bg-base-200/30 font-medium text-sm">{m.pair()}</th>
            <th class="bg-base-200/30 font-medium text-sm">{m.timeframe()}</th>
            <th class="bg-base-200/30 font-medium text-sm text-right">{m.price()}</th>
            <th class="bg-base-200/30 font-medium text-sm text-right">{m.order_num()}</th>
            <th class="bg-base-200/30 font-medium text-sm text-right">{m.recent_time()}({curTZ()})</th>
            {#if selectedStrategy !== 'all'}
              <th class="bg-base-200/30 font-medium text-sm">{m.settings()}</th>
              <th class="bg-base-200/30 font-medium text-sm">{m.actions()}</th>
            {:else}
              <th class="bg-base-200/30 font-medium text-sm">{m.strategy()}</th>
            {/if}
          </tr>
        </thead>
        <tbody>
          {#each getPairsByStrategy(selectedStrategy) as item}
            <tr class="hover:bg-base-200/50 transition-colors">
              <td class="text-sm font-mono">{item.pair}</td>
              <td class="text-sm">{item.tf || item.tfs}</td>
              <td class="text-sm font-mono text-right">{item.price}</td>
              <td class="text-sm font-mono text-right">{item.odNum || item.totalOdNum}</td>
              <td class="text-sm font-mono text-right">{fmtDateStrTZ(item.lastBarMS)}</td>
              {#if selectedStrategy !== 'all'}
                <td class="text-sm text-base-content/70">{showJobArgs(item)}</td>
                <td>
                  {#if Array.isArray(item.args)}
                    <button class="btn btn-xs bg-primary/90 hover:bg-primary text-primary-content border-none" 
                            onclick={() => editJobArgs(item)}>
                      {m.edit()}
                    </button>
                  {/if}
                </td>
              {:else}
                <td class="text-sm">
                  <div title={item.strategies.join(', ')}>
                    {item.strategyCount}
                  </div>
                </td>
              {/if}
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  </div>
</div>