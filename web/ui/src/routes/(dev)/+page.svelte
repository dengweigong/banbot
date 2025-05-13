<script lang="ts">
  import * as m from '$lib/paraglide/messages.js'
  import Icon from '$lib/Icon.svelte'
  import {getLocale, localizeHref} from "$lib/paraglide/runtime";
  import {onMount} from "svelte";
  import {getApi} from "$lib/netio";
  import {goto} from "$app/navigation";

  let showQRCode = $state(false)

  onMount(() => {
    // /ping可访问表示处于实时模式，跳转到DashBoard
    getApi('/ping').then(rsp => {
      if (rsp.code == 200){
        goto(localizeHref('/dash'))
      }
    })
  })
</script>

<main class="flex-1 flex items-center justify-center">
  <div class="max-w-4xl w-full px-4">
    <div class="text-center mb-16">
      <p class="text-xl mb-2">
        {m.need_help_doc()}
        <a class="text-blue-500 hover:text-blue-600" href="https://docs.banbot.site/{getLocale()}">
          {m.document()}
        </a>
      </p>
    </div>

    <div class="grid grid-cols-1 sm:grid-cols-2 gap-6">
      <div class="flex items-start space-x-4">
        <div class="bg-blue-500 rounded-lg p-3 text-white">
          <Icon name="academic"/>
        </div>
        <div>
          <h3 class="font-semibold text-lg mb-2">
            <a href="https://docs.banbot.site{getLocale()}" target="_blank">{m.document()}</a>
          </h3>
          <p class="text-gray-600">
            {m.doc_desc()}
          </p>
        </div>
      </div>

      <div class="flex items-start space-x-4">
        <div class="bg-gray-800 rounded-lg p-3 text-white">
          <Icon name="code"/>
        </div>
        <div>
          <h3 class="font-semibold text-lg mb-2">
            <a href="https://github.com/banbox/banbot" target="_blank">Github</a>
          </h3>
          <p class="text-gray-600">{m.github_desc()}</p>
        </div>
      </div>

      <div class="flex items-start space-x-4">
        <div class="bg-green-500 rounded-lg p-3 text-white">
          <Icon name="wechat"/>
        </div>
        <div class="relative">
          <h3 class="font-semibold text-lg mb-2"
             onmouseenter={() => showQRCode = true}
             onmouseleave={() => showQRCode = false}>
            {m.wechat_group()}
          </h3>
          {#if showQRCode}
            <div class="absolute z-10 left-0 top-8">
              <img 
                src="/img/wechat.jpg" 
                alt="WeChat QR Code"
                class="w-48 h-48 rounded-lg shadow-lg"
              />
            </div>
          {/if}
          <p class="text-gray-600">{m.wechat_desc()}</p>
        </div>
      </div>

      <div class="flex items-start space-x-4">
        <div class="bg-indigo-500 rounded-lg p-3 text-white">
          <Icon name="discord"/>
        </div>
        <div>
          <h3 class="font-semibold text-lg mb-2">
            <a href="https://discord.com/invite/XXjA8ctqga" target="_blank">Discord</a>
          </h3>
          <p class="text-gray-600">{m.discord_desc()}</p>
        </div>
      </div>
    </div>
  </div>
</main>