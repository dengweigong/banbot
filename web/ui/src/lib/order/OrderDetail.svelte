<script lang="ts">
  import { fmtDateStrTZ } from '$lib/dateutil';
  import * as m from '$lib/paraglide/messages';
  import type { InOutOrder } from '$lib/order/types';
  import Modal from '../kline/Modal.svelte';
  import FormField from './FormField.svelte';

  let { 
    show=$bindable(false),
    order=undefined,
    editable=false,
   }:{
    show: boolean;
    order?: InOutOrder | null;
    editable: boolean;
  } = $props();

  const InOutStatus = [m.no_enter(), m.part_enter(), m.full_enter(), m.part_exit(), m.full_exit(), m.deleted()];

  const OdStatus = [m.no_filled(), m.part_ok(), m.closed()];
</script>

<Modal title={m.order_details()} bind:show={show} width={1200}>
  {#if order}
  <div class="space-y-6">
    <!-- 基本信息 -->
    <div class="grid grid-cols-3 gap-x-3 gap-y-1 p-5 border rounded-lg">
      <FormField label="ID" bind:value={order.id} />
      <FormField label="Task ID" bind:value={order.task_id} />
      <FormField label={m.pair()} bind:value={order.symbol}/>
      <FormField label={m.timeframe()} bind:value={order.timeframe}/>
      <FormField label={m.side()} value={order.short ? m.short() : m.long()} />
      <FormField label={m.leverage()} value={`${order.leverage}x`} />
      <FormField label={m.strategy()} value={`${order.strategy}:${order.stg_ver}`} />
      <FormField label={m.status()} value={InOutStatus[order.status]} />
    </div>
  
    <!-- 入场订单信息 -->
    {#if order.enter}
    <div class="border rounded-lg p-5">
      <h3 class="text-lg font-bold mb-4">{m.enter_order()}</h3>
      <div class="grid grid-cols-3 gap-x-3 gap-y-1">
        <FormField label={m.enter_time()} value={order.enter_at} />
        <FormField label={m.enter_time()} value={fmtDateStrTZ(order.enter_at)} />
        <FormField label={m.update_time()} value={fmtDateStrTZ(order.enter?.update_at)} />
        <FormField label={m.enter_tag()} bind:value={order.enter_tag} />
        <FormField label={m.order_type()} bind:value={order.enter.order_type} />
        <FormField label={m.side()} bind:value={order.enter.side} {editable} />
        <FormField label={m.order_id()} bind:value={order.enter.order_id} />
        <FormField label={m.init_price()} value={order.init_price?.toFixed(7)} />
        <FormField label={m.enter_price()} value={order.enter.price?.toFixed(7)} {editable} />
        <FormField label={m.amount()} value={order.enter.amount?.toFixed(5)} {editable} />
        <FormField label={m.cost()} value={order.quote_cost?.toFixed(5) ?? '-'} {editable} />
        {#if order.enter.filled > 0}
        <FormField label={m.average_price()} value={order.enter.average?.toFixed(7)} />
        <FormField label={m.filled_amount()} value={order.enter.filled?.toFixed(5)}/>
        <FormField label={m.fee()} value={`${order.enter.fee?.toFixed(5)} ${order.enter.fee_type}`} />
        {/if}
        <FormField label={m.status()} value={OdStatus[order.enter.status]} {editable} />
      </div>
    </div>
    {/if}
  
    <!-- 出场订单信息 -->
    {#if order.exit_tag && order.exit}
      <div class="border rounded-lg p-5">
        <h3 class="text-lg font-bold mb-4">{m.exit_order()}</h3>
        <div class="grid grid-cols-3 gap-x-3 gap-y-1">
          <FormField label={m.exit_time()} value={order.exit_at} />
          <FormField label={m.exit_time()} value={fmtDateStrTZ(order.exit_at)} />
          <FormField label={m.update_time()} value={fmtDateStrTZ(order.exit.update_at)} />
          <FormField label={m.exit_tag()} bind:value={order.exit_tag} {editable} />
          <FormField label={m.order_type()} bind:value={order.exit.order_type} {editable} />
          <FormField label={m.side()} bind:value={order.exit.side} {editable} />
          <FormField label={m.order_id()} bind:value={order.exit.order_id} />
          <FormField label={m.exit_price()} value={order.exit.price?.toFixed(7)} {editable} />
          <FormField label={m.amount()} value={order.exit.amount?.toFixed(5)} {editable} />
          {#if order.exit.filled > 0}
            <FormField label={m.average_price()} value={order.exit.average?.toFixed(7)} />
            <FormField label={m.filled_amount()} value={order.exit.filled?.toFixed(5)} />
            <FormField label={m.fee()} value={`${order.exit.fee?.toFixed(5)} ${order.exit.fee_type}`} />
          {/if}
          <FormField label={m.status()} value={OdStatus[order.exit.status]} />
        </div>
      </div>
    {/if}
  
    <!-- 盈亏信息 -->
    <div class="border rounded-lg p-5">
      <h3 class="text-lg font-bold mb-4">{m.performance()}</h3>
      <div class="grid grid-cols-3 gap-x-3 gap-y-1">
        <FormField label={m.max_pft_rate()} value={`${(order.max_pft_rate * 100)?.toFixed(2)}%`} />
        <FormField label={m.max_draw_down()} value={`${order.max_draw_down?.toFixed(2)}%`} />
        <FormField label={m.profit()} value={order.profit?.toFixed(5)} />
        <FormField label={m.profit_rate()} value={`${(order.profit_rate * 100)?.toFixed(2)}%`} />
        {#if order.status < 4}
          <FormField label={m.cur_price()} value={order.curPrice?.toFixed(7) ?? '-'} />
        {/if}
      </div>
    </div>
  
    <!-- 额外信息 -->
    {#if order.info}
      <div class="border rounded-lg p-5">
        <h3 class="text-lg font-bold mb-4">{m.additional_info()}</h3>
        <pre class="whitespace-pre-wrap break-all bg-base-200 p-4 rounded-lg text-sm">{JSON.stringify(order.info, null, 2)}</pre>
      </div>
    {/if}
  </div> 
{/if}
</Modal>