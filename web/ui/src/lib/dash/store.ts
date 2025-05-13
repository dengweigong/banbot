import { get, writable } from 'svelte/store';
import { persisted } from 'svelte-persisted-store';
import type { BotTicket, BotAccount } from './types';
import { DashCtx, DashSave } from './context';
import { getAccApi } from '$lib/netio';
import {site} from '$lib/stores/site';

// 创建全局状态stores
export const ctx = writable<DashCtx>(new DashCtx());
export const save = persisted('dash', new DashSave());
export const acc = writable<BotAccount>({
  id: '',
  url: '',
  name: '',
  account: '',
  role: '',
  token: '',
})

export function activeAcc(accInfo: BotAccount){
  acc.set(accInfo)
  save.update(s => {
    s.current = accInfo.id;
    return s
  })
}

// 更新账户信息
export async function updateAcc(accInfo: BotAccount) {
  site.update(s => {
    s.apiHost = accInfo.url;
    s.apiReady = true;
    return s;
  });
  acc.set(accInfo);
  const rsp = await getAccApi('/today_num');
  if (rsp.code === 200) {
      accInfo.status = rsp.running ? 'running' : 'stopped';
      accInfo.dayDoneNum = rsp.dayDoneNum;
      accInfo.dayDonePft = rsp.dayDonePft;
      accInfo.dayOpenNum = rsp.dayOpenNum;
      accInfo.dayOpenPft = rsp.dayOpenPft;
  } else if (rsp.code === 401) {
    accInfo.role = 'del';
  } else if(rsp.msg && rsp.msg.indexOf("FetchError") >= 0){
    accInfo.status = 'disconnected';
  } else {
      console.error('update acc status fail', acc, rsp)
  }
}

// 加载机器人账户信息
export async function loadBotAccounts(bot: BotTicket, request: boolean = false): Promise<boolean> {
  if (!bot.accounts) return false;
  const accounts = get(ctx).accounts;
  let hasValid = false;
  
  let allowApi = true;
  for (const account in bot.accounts) {
    if (!bot.accounts.hasOwnProperty(account)) {
      continue
    }
    const role = bot.accounts[account];
    const accInfo: BotAccount = {
      id: `${bot.url}_${account}`,
      url: bot.url,
      name: bot.name || '',
      account: account,
      role: role,
      token: bot.token || '',
      env: bot.env || ''
    };
    if (request) {
      if(allowApi){
        await updateAcc(accInfo)
        if(accInfo.role === 'del')continue;
        if(accInfo.status === 'disconnected'){
          allowApi = false;
        }
      }else{
        accInfo.status = 'disconnected';
      }
    }

    const existingIdx = accounts.findIndex(a =>
      a.url === bot.url && a.account === account
    );
    hasValid = true;

    if (existingIdx >= 0) {
      // 如果新角色是admin且旧角色不是admin，则更新
      const old = accounts[existingIdx];
      if (role === 'admin' && old.role !== 'admin') {
        old.role = 'admin';
        old.token = bot.token || old.token;
      }
    } else {
      // 新账户直接添加
      accounts.push(accInfo);
    }
  }
  ctx.update(c => {
      c.accounts = accounts;
      return c;
  });
  if(!hasValid){
    bot.name = 'del';
  }
  return hasValid;
} 

export async function loadAccounts(request: boolean = false) {
  const saveShot = get(save);
  const tickets = saveShot.tickets;
  await Promise.all(tickets.map(async bot => await loadBotAccounts(bot, request)));
  const validTickets = tickets.filter(t => t.name !== 'del');
  if (validTickets.length < tickets.length) {
    save.update(s => {
      s.tickets = validTickets;
      s.current = '';
      return s;
    });
  }
  const accounts = get(ctx).accounts;
  const selected = accounts.filter(a => a.id === saveShot.current);
  if (selected.length > 0) {
    acc.set(selected[0]);
  }else if(accounts.length > 0){
    activeAcc(accounts[0]);
  }else{
    //console.log('no accounts avaiable', saveShot);
  }
}