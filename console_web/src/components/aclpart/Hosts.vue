<script setup>
import { watch, ref, onMounted } from "vue";
import Toast from "../Toast.vue";
import SetHost from "./SetHost.vue";

const toastShow = ref(false);
const toastMsg = ref("");
watch(toastShow, () => {
  if (toastShow.value) {
    setTimeout(function () {
      toastShow.value = false;
    }, 5000);
  }
});

const hosts = ref([]);

function reloadHosts() {
  return axios
    .get("/admin/api/acls/hosts")
    .then(function (response) {
      if (response.data["status"] == "success") {
        hosts.value = response.data["data"]["hosts"];
      } else {
        toastMsg.value = "获取主机别名失败:" + response.data["status"].substring(6);
        toastShow.value = true;
      }
    })
    .catch(function (error) {
      toastMsg.value = "获取主机别名失败:" + error;
      toastShow.value = true;
    });
}

onMounted(() => {
  reloadHosts();
});

const setHostShow = ref(false);
const currentHost = ref(null);

function showCreateHost() {
  currentHost.value = null;
  setHostShow.value = true;
}

function showEditHost(host) {
  currentHost.value = JSON.parse(JSON.stringify(host));
  setHostShow.value = true;
}

function saveHostDone() {
  reloadHosts();
}

const wantRemoveHost = ref("");
const deleteHostShow = ref(false);

function toRemoveHost(host) {
  wantRemoveHost.value = host;
  deleteHostShow.value = true;
}

function doRemoveHost() {
  axios
    .delete("/admin/api/acls/hosts/" + wantRemoveHost.value, {})
    .then(function (response) {
      if (response.data["status"] == "success") {
        var tmpHosts = [];
        for (var i in hosts.value) {
          if (hosts.value[i].hostName != response.data["data"]) {
            tmpHosts.push(hosts.value[i]);
          }
        }
        hosts.value = tmpHosts;
        deleteHostShow.value = false;
      } else {
        toastMsg.value = "删除主机别名失败:" + response.data["status"].substring(6);
        toastShow.value = true;
      }
    })
    .catch(function (error) {
      toastMsg.value = "删除主机别名失败:" + error;
      toastShow.value = true;
    });
}
</script>

<template>
  <div class="flex-1">
    <div class="text-3xl font-semibold tracking-tight leading-tight mb-2 flex items-center">
      <h1 class="mr-2" tabindex="-1">主机别名</h1>
    </div>
    <div class="text-gray-600 mt-3 mb-10">
      <p>查看和管理 ACL 中可复用的<strong>主机别名</strong>(<strong>Hosts</strong>)</p>
      <p class="mt-2">
        主机别名可把单个 IP 或 CIDR 网段定义成一个短名称，后续在 ACL 规则目标中复用。
      </p>
    </div>
    <div class="mt-10">
      <div class="flex justify-between items-center mt-16">
        <div>
          <h3 class="text-xl font-semibold tracking-tight">现有主机别名</h3>
          <p class="text-gray-600">别名对应一个 IP 地址或 CIDR 网段，例如 <code>100.64.0.10</code> 或 <code>10.0.0.0/24</code></p>
        </div>
        <button
          @click="showCreateHost"
          class="btn border border-stone-300 hover:border-stone-300 disabled:border-stone-300 bg-base-200 hover:bg-base-300 disabled:bg-base-200/60 text-black disabled:text-black/30 h-9 min-h-fit ml-3 font-normal"
        >
          创建主机别名…
        </button>
      </div>
      <div
        v-if="!hosts || hosts.length == 0"
        class="rounded-md border border-stone-200 mt-4 bg-stone-50 p-6"
      >
        <div class="flex justify-center">
          <div class="w-full text-center max-w-xl text-gray-500">你还没有任何主机别名</div>
        </div>
      </div>

      <table v-if="hosts && hosts.length > 0" class="block border box-border rounded-lg mt-4 tb">
        <thead class="block font-semibold tracking-wider text-left text-xs text-stone-500">
          <tr class="flex border-b border-stone-200 pl-8 pr-4 lg:px-4">
            <th class="w-44 shrink-0 py-2">别名</th>
            <th class="flex-1 shrink-0 py-2 min-w-0">IP / 网段</th>
            <th class="w-32 shrink-0 py-2 text-right">操作</th>
          </tr>
        </thead>
        <tbody class="block">
          <template v-for="(host, i) in hosts" :key="host.hostName">
            <tr
              :class="{ 'border-t': i > 0 }"
              class="group flex border-stone-200 hover:bg-gray-50 pl-8 pr-4 lg:px-4 lg:cursor-auto border-b-0"
            >
              <td class="flex shrink-0 py-2 w-44">
                <pre class="text-sm truncate leading-6 font-semibold"><code>{{ host.hostName }}</code></pre>
              </td>
              <td class="flex-1 shrink-0 py-2 min-w-0">
                <pre class="text-sm truncate leading-6"><code>{{ host.ipPrefix }}</code></pre>
              </td>
              <td class="w-32 shrink-0 py-2 text-right">
                <button
                  @click="showEditHost(host)"
                  type="button"
                  class="text-blue-500 hover:text-blue-700 mr-3"
                >
                  编辑…
                </button>
                <button
                  @click="toRemoveHost(host.hostName)"
                  type="button"
                  class="text-red-400 hover:text-red-600"
                >
                  删除…
                </button>
              </td>
            </tr>
          </template>
        </tbody>
      </table>
    </div>
  </div>
  <Teleport to="body">
    <SetHost
      v-if="setHostShow"
      :host="currentHost"
      @saved-host="saveHostDone"
      @close="setHostShow = false"
    ></SetHost>
    <template v-if="deleteHostShow">
      <div
        @click.self="deleteHostShow = false"
        class="fixed overflow-y-auto inset-0 py-8 z-30 bg-gray-900 bg-opacity-[0.07]"
        style="pointer-events: auto"
      >
        <div
          class="bg-white rounded-lg relative p-4 md:p-6 text-gray-700 max-w-lg min-w-[19rem] my-8 mx-auto w-[97%] shadow-2xl"
          style="pointer-events: auto"
        >
          <header class="flex items-center justify-between space-x-4 mb-5 mr-8">
            <div class="font-semibold text-lg truncate">删除主机别名</div>
          </header>
          <form @submit.prevent="doRemoveHost">
            <p class="text-gray-700 mb-4">
              删除此前请确保 ACL 规则中已不再引用该别名。
            </p>
            <footer class="flex mt-10 justify-end space-x-4">
              <button
                @click="deleteHostShow = false"
                class="btn border border-base-300 hover:border-base-300 bg-base-200 hover:bg-base-300 text-black h-9 min-h-fit"
                type="button"
              >
                取消
              </button>
              <button
                class="btn border-0 bg-red-600 hover:bg-red-700 text-white h-9 min-h-fit"
                type="submit"
              >
                删除主机别名
              </button>
            </footer>
          </form>
          <button
            @click="deleteHostShow = false"
            class="btn btn-sm btn-ghost absolute top-5 right-5 px-2 py-2 border-0 bg-base-0 focus:bg-base-200 hover:bg-base-200"
            type="button"
          >
            <svg
              xmlns="http://www.w3.org/2000/svg"
              width="1.25em"
              height="1.25em"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              stroke-width="2"
              stroke-linecap="round"
              stroke-linejoin="round"
            >
              <line x1="18" y1="6" x2="6" y2="18"></line>
              <line x1="6" y1="6" x2="18" y2="18"></line>
            </svg>
          </button>
        </div>
      </div>
    </template>
  </Teleport>

  <Teleport to=".toast-container">
    <Toast :show="toastShow" :msg="toastMsg" @close="toastShow = false"></Toast>
  </Teleport>
</template>

<style scoped></style>
