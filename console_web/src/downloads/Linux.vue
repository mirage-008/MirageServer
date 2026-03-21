<script setup>
const props = defineProps({
  downloadDetails: Object,
});
</script>
<template>
  <div v-if="downloadDetails.linux.primary == ''" class="text-center">
    <code class="rounded-md border border-stone-200 gap-2 max-w-sm bg-stone-50 p-2">
      暂未提供Linux版
    </code>
  </div>
  <div
    v-if="downloadDetails.linux.primary != ''"
    class="pt-2 pb-4 border-b border-gray-200 max-w-xl mx-auto"
  >
    <div class="text-center">
      <code class="rounded-md border border-stone-200 gap-2 max-w-sm bg-stone-50 p-2">
        当前最新版本 {{ downloadDetails.linux.primary.split("-")[0] }}
      </code>
    </div>
  </div>
  <div v-if="downloadDetails.linux.primary != ''" class="mx-auto max-w-xl">
    <header class="my-4">
      <h2 class="text-xl font-medium">使用Repo安装 - 适用于DEB及RPM类型包管理器</h2>
    </header>
    <div class="Markdown mb-4">
      <ol>
        <li>添加软件源</li>
        <p class="pb-2">DEB环境（即apt管理）</p>
        <div
          class="rounded-md border border-stone-200 gap-2 bg-stone-50 p-2 overflow-x-scroll whitespace-nowrap"
        >
          curl -fsSL https://{{
            downloadDetails.linux.secondary
          }}/download/deb/mirage.list | sudo tee /etc/apt/sources.list.d/mirage.list
        </div>
        <p class="pb-2">RPM环境（即yum/dnf管理）</p>
        <div
          class="rounded-md border border-stone-200 gap-2 bg-stone-50 p-2 overflow-x-scroll whitespace-nowrap"
        >
          sudo yum-config-manager --add-repo https://{{
            downloadDetails.linux.secondary
          }}/download/rpm/mirage.repo
        </div>
        <p class="pb-2">或</p>
        <div
          class="rounded-md border border-stone-200 gap-2 bg-stone-50 p-2 overflow-x-scroll whitespace-nowrap"
        >
          sudo dnf config-manager --add-repo https://{{
            downloadDetails.linux.secondary
          }}/download/rpm/mirage.repo
        </div>
        <li>进行蜃境客户端安装</li>
        <p class="pb-2">DEB环境（即apt管理）</p>
        <div
          class="rounded-md border border-stone-200 gap-2 bg-stone-50 p-2 overflow-x-auto whitespace-nowrap"
        >
          sudo apt update && sudo apt install mirage
        </div>
        <p class="pb-2">RPM环境（即yum/dnf管理）</p>
        <div
          class="rounded-md border border-stone-200 gap-2 bg-stone-50 p-2 overflow-x-auto whitespace-nowrap"
        >
          sudo yum install mirage
        </div>
        <p class="pb-2">或</p>
        <div
          class="rounded-md border border-stone-200 gap-2 bg-stone-50 p-2 overflow-x-auto whitespace-nowrap"
        >
          sudo dnf install mirage
        </div>
        <p class="pb-2">如服务未自动启动，可手动启用 miraged 服务</p>
        <div
          class="rounded-md border border-stone-200 gap-2 bg-stone-50 p-2 overflow-x-auto whitespace-nowrap"
        >
          sudo systemctl enable --now miraged
        </div>
        <li>接下来您就可以使用蜃境Linux版本命令了，例如使用下面命令登录接入</li>
        <div
          class="rounded-md border border-stone-200 gap-2 bg-stone-50 p-2 overflow-x-auto whitespace-nowrap"
        >
          sudo mirage up
        </div>
      </ol>
    </div>
  </div>
  <div v-if="downloadDetails.linux.primary != ''" class="mx-auto max-w-xl">
    <header class="my-4">
      <h2 class="text-xl font-medium">使用二进制发行包 - 适用于其他类型Linux环境</h2>
    </header>
    <div class="Markdown mb-4">
      <ol>
        <li>下载二进制发行包</li>
        <div class="pt-2">i386架构</div>
        <a
          :href="
            'https://' +
            downloadDetails.linux.secondary +
            '/download/tgz/mirage_' +
            downloadDetails.linux.primary.split('-')[0] +
            '_386.tgz'
          "
          >mirage_{{ downloadDetails.linux.primary.split("-")[0] }}_386.tgz</a
        >
        <div>amd64架构</div>
        <a
          :href="
            'https://' +
            downloadDetails.linux.secondary +
            '/download/tgz/mirage_' +
            downloadDetails.linux.primary.split('-')[0] +
            '_amd64.tgz'
          "
          >mirage_{{ downloadDetails.linux.primary.split("-")[0] }}_amd64.tgz</a
        >
        <div>arm架构</div>
        <a
          :href="
            'https://' +
            downloadDetails.linux.secondary +
            '/download/tgz/mirage_' +
            downloadDetails.linux.primary.split('-')[0] +
            '_arm.tgz'
          "
          >mirage_{{ downloadDetails.linux.primary.split("-")[0] }}_arm.tgz</a
        >
        <div>arm64架构</div>
        <a
          class="pb-2"
          :href="
            'https://' +
            downloadDetails.linux.secondary +
            '/download/tgz/mirage_' +
            downloadDetails.linux.primary.split('-')[0] +
            '_arm64.tgz'
          "
          >mirage_{{ downloadDetails.linux.primary.split("-")[0] }}_arm64.tgz</a
        >
        <li>解压缩发行包</li>
        <div
          class="rounded-md border border-stone-200 gap-2 bg-stone-50 p-2 overflow-x-auto whitespace-nowrap"
        >
          tar xvf 发行包名称
        </div>
        <p class="pb-2">注意：openwrt之上需要使用下面命令</p>
        <div
          class="rounded-md border border-stone-200 gap-2 bg-stone-50 p-2 overflow-x-auto whitespace-nowrap"
        >
          tar x -zvf 发行包名称
        </div>
        <li>使用</li>
        <div class="pb-2">
          解压之后可按需将 mirage 复制到
          <div class="rounded-md border border-stone-200 bg-stone-50 px-1 inline-block">
            /usr/bin
          </div>
          、将 miraged 复制到
          <div class="rounded-md border border-stone-200 bg-stone-50 px-1 inline-block">
            /usr/sbin
          </div>
          ，并使用解压出的 systemd 目录中的 miraged.service 与 miraged.defaults
          配置后台服务。
        </div>
      </ol>
    </div>
  </div>
</template>
