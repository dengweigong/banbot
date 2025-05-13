<script lang="ts">
  import * as m from '$lib/paraglide/messages.js'
  import Icon from '$lib/Icon.svelte';
  import {page} from '$app/state';
  let { children } = $props();
  import {site} from '$lib/stores/site';
  import {dev} from '$app/environment';
  import {initWsConn} from '$lib/dev/websocket';
  import {browser} from '$app/environment';
  import {alerts} from '$lib/stores/alerts';
  import { getApi } from '$lib/netio';
  import {localizeHref} from "$lib/paraglide/runtime";
  import {clickCompile} from "$lib/dev/common";

  const menuItems = [
    { href: localizeHref('/strategy'), icon: 'code', label: m.strategy(), tag: '/strategy' },
    { href: localizeHref('/backtest/new'), icon: 'calculate', label: m.backtest(), tag: '/backtest' },
    { href: localizeHref('/data'), icon: 'chart-bar', label: m.data(), tag: '/data' },
    { href: localizeHref('/trade'), icon: 'banknotes', label: m.live_trading(), tag: '/trade' },
    //{ href: localizeHref('/tools'), icon: 'tool', label: m.tools() },
    //{ href: localizeHref('/optimize'), icon: 'cpu', label: m.optimize() },
  ];
  
  site.update((s) => {
    if(dev){
      s.apiHost = 'http://localhost:8000';
    }
    s.apiReady = true;
    return s;
  });
  
  if(browser){
    initWsConn();
  }
  
  let showLogs = $state(false);
  let logs = $state('');
  let lastStart: number | null = null;

  async function fetchLogs(end?: number) {
    const params = end ? { end } : {};
    const res = await getApi('/dev/logs', params);
    if (res.code === 200) {
      if (end) {
        logs = logs + res.data;
      } else {
        logs = res.data;
      }
      lastStart = res.start;
    } else {
      console.error('get logs failed', res);
      alerts.error(res.msg || 'get logs failed');
    }
  }

  function toggleLogs() {
    showLogs = !showLogs;
    if (showLogs && !logs) {
      fetchLogs();
    }
  }

</script>

<div class="flex flex-col min-h-screen">
  <div class="navbar bg-base-100 shadow-md py-0 relative">
    <div class="navbar-start">
      <a href={localizeHref("/")} class="text-xl font-bold text-primary">
        <img src="/banbot.png" alt="logo" class="w-24 h-10 object-contain"/>
      </a>
    </div>
    
    <div class="navbar-center">
      <!-- 移动端的下拉菜单 -->
      <div class="dropdown lg:hidden">
        <div tabindex="0" role="button" class="btn btn-ghost">
          <Icon name="horz3"/>
        </div>
        <ul tabindex="0" class="menu w-full menu-sm dropdown-content bg-base-100 rounded-box z-[1] mt-3 w-52 p-2 shadow">
          {#each menuItems as {href, icon, label, tag}}
            <li>
              <a href={href} class="py-3" class:text-primary={page.url.pathname.includes(tag)}>
                <Icon name={icon}/>{label}
              </a>
            </li>
          {/each}
        </ul>
      </div>
      
      <!-- 桌面端的水平菜单 -->
      <ul class="menu w-full menu-horizontal px-1 hidden lg:flex">
        {#each menuItems as {href, icon, label, tag}}
          <li>
            <a href={href} class:text-primary={page.url.pathname.includes(tag)}>
              <Icon name={icon}/>{label}
            </a>
          </li>
        {/each}
      </ul>
    </div>
    
    <div class="navbar-end">
      <div class="tooltip tooltip-bottom" data-tip={m.build()}>
        <button class="btn btn-ghost btn-circle" class:animate-spin={$site.building} onclick={clickCompile}>
          <Icon name="refresh" class={$site.compileNeed ? 'gradient size-6' : 'size-6'}/>
        </button>
      </div>
      <div class="tooltip tooltip-bottom" data-tip={m.setting()}>
        <a href={localizeHref("/setting")} class="btn btn-ghost btn-circle">
          <Icon name="setting"/>
        </a>
      </div>
      <div class="tooltip tooltip-bottom" data-tip={m.logs()}>
        <button class="btn btn-ghost btn-circle" onclick={toggleLogs}>
          <Icon name="document-text"/>
        </button>
      </div>
    </div>

    {#if $site.heavyProgress > 0 && $site.heavyProgress < 1 }
      <div class="absolute bottom-0 left-0 w-full">
        <progress class="progress progress-info w-full h-[2px] opacity-80" value={$site.heavyProgress} max=1></progress>
      </div>
    {/if}
  </div>

  <!-- 添加日志抽屉 -->
  <div class="drawer drawer-end flex-1 flex flex-col">
    <input id="logs-drawer" type="checkbox" class="drawer-toggle" bind:checked={showLogs} />
    
    <div class="drawer-content flex-1 flex flex-col">
      <!-- 主页面内容 -->
      <main class="flex-1 flex flex-col">
        {@render children()}
      </main>
    </div>
    
    <div class="drawer-side">
      <label for="logs-drawer" aria-label="close logs" class="drawer-overlay"></label>
      <div class="bg-base-200 w-2/3 h-full p-4 flex flex-col">
        <!-- 日志抽屉顶部按钮 -->
        <div class="flex justify-end gap-2 mb-4">
          <button class="btn btn-ghost btn-sm" 
                  onclick={() => lastStart && fetchLogs(lastStart)}>
            <Icon name="arrow-down"/>{m.load_more()}
          </button>
          <button class="btn btn-ghost btn-sm" 
                  onclick={() => fetchLogs()}>
            <Icon name="refresh"/>{m.refresh()}
          </button>
        </div>
        
        <!-- 日志内容 -->
        <pre class="flex-1 overflow-auto whitespace-pre-wrap break-all">{logs}</pre>
      </div>
    </div>
  </div>
</div>
