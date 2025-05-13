<script lang="ts">
  import * as m from '$lib/paraglide/messages.js'
  import type { BotTicket } from '$lib/dash/types';
  import {postApi} from '$lib/netio'
  import { browser } from '$app/environment';
  import {loadBotAccounts, save} from '$lib/dash/store';

  let submitting = $state(false);
  let errorMsg = $state('');
  let urlPlaceholder = $state('');

  let {newBot}: {newBot: (m: BotTicket) => void} = $props()

  let empty: BotTicket = {
    name: '',
    url: '',
    user_name: '',
    password: '',
    env: '',
    token: '',
    accounts: {},
  };

  let model: BotTicket = $state(empty);

  if (browser) {
    urlPlaceholder = `${window.location.protocol}//12.34.56.78:8001`;
  }

  function validateUrl(value: string): string | null {
    if (!value) return m.this_is_required();
    if (!/^(https?:\/\/)\S+/.test(value)) return m.not_url();
    return null;
  }

  function validateRequired(value: string): string | null {
    return value ? null : m.this_is_required();
  }

  async function handleSubmit(e: Event) {
    e.preventDefault();
    const urlError = validateUrl(model.url);
    const userError = validateRequired(model.user_name);
    const passError = validateRequired(model.password);

    if (urlError || userError || passError) {
      errorMsg = [urlError, userError, passError].filter(Boolean)[0] || '';
      return;
    }

    submitting = true;
    errorMsg = '';

    try {
      const data = await postApi(`${model.url}/api/login`, {
        username: model.user_name,
        password: model.password,
      });
      if (data.code === 200) {
        model.name = data.name;
        model.token = data.token;
        model.env = data.env;
        model.accounts = data.accounts || {};
        const oldNum = $save.tickets.filter(bot => bot.url === model.url && bot.user_name === model.user_name).length;
        const info = $state.snapshot(model);
        if (oldNum == 0) {
          save.update(s => {
            s.tickets.push(info);
            return s;
          });
        }
        await loadBotAccounts(info);
        newBot(info);
      } else if (data.code === 401) {
        errorMsg = m.bad_user_pwd();
      } else {
        errorMsg = m.bad_url({url: model.url});
        if (model.url !== window?.location.origin) {
          errorMsg += `<br>${m.bad_url_cors({url: window.location.origin})}`;
        }
      }
    } catch (error) {
      errorMsg = m.connect_failed({err: error as string});
      console.error('login error', error);
    } finally {
      submitting = false;
    }
  }
</script>

<div class="max-w-md mx-auto">
  <form onsubmit={handleSubmit} class="space-y-4">
    <fieldset class="fieldset">
      <label class="label" for="bot_url">
        <span>{m.bot_url()}</span>
      </label>
      <input
        id="bot_url"
        type="text"
        bind:value={model.url}
        placeholder={urlPlaceholder}
        class="input w-full"
      />
    </fieldset>

    <fieldset class="fieldset">
      <label class="label" for="user_name">
        <span>{m.user_name()}</span>
      </label>
      <input
        id="user_name"
        type="text"
        bind:value={model.user_name}
        placeholder={m.input_username()}
        class="input w-full"
      />
    </fieldset>

    <fieldset class="fieldset">
      <label class="label" for="password">
        <span>{m.password()}</span>
      </label>
      <input
        id="password"
        type="password"
        bind:value={model.password}
        placeholder={m.input_password()}
        class="input w-full"
      />
    </fieldset>

    <button
      type="submit"
      class="btn btn-primary btn-block"
      disabled={submitting}
    >
      {#if submitting}
        <span class="loading loading-spinner"></span>
      {/if}
      {m.login_bot()}
    </button>
  </form>

  {#if errorMsg}
    <div class="mt-4 text-center text-error text-sm whitespace-pre-line">
      {@html errorMsg}
    </div>
  {/if}
</div> 