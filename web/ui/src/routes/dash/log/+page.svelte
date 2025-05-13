<script lang="ts">
  import { onMount } from 'svelte';
  import {getAccApi} from '$lib/netio';
  import * as m from '$lib/paraglide/messages';
  import {alerts} from "$lib/stores/alerts";

  let logs = $state('');
  let logStart = $state(-1);
  let loadingLogs = $state(false);

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
    const rsp = await getAccApi('/log', {
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

  onMount(() => {
    loadLogs(true);
  });
</script>

<div class="card bg-base-100">
  <div class="card-body p-4">
    <div class="flex justify-between items-center mb-3">
      <h2 class="text-xl font-semibold text-base-content">{m.system_logs()}</h2>
      <button class="btn btn-sm btn-primary" onclick={() => loadLogs(true)}>
        {m.refresh()}
      </button>
    </div>
    
    <div class="bg-base-200/50 rounded-lg p-4">
      <a class="link link-primary link-hover" onclick={() => loadLogs(false)}>{loadingLogs ? m.loading() : m.load_more()}</a>
      <pre class="whitespace-pre-wrap break-all font-mono text-sm text-base-content/80">{logs}</pre>
    </div>
  </div>
</div>
