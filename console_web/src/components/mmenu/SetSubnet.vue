<script setup>
import { watch, ref, onMounted, onBeforeUpdate, computed } from "vue";
import { useDisScroll } from "/src/utils.js";

const emit = defineEmits(["update-done", "update-fail"]);

useDisScroll();
const inputBlocking = ref(false);

const props = defineProps({
  id: String,
  currentMachine: Object,
});

const hasAllowedSubnet = computed(() => {
  return props.currentMachine.allowedIPs && props.currentMachine.allowedIPs.length > 0;
});
const hasExtraSubnet = computed(() => {
  return props.currentMachine.extraIPs && props.currentMachine.extraIPs.length > 0;
});
const viaPreviewPrefix = ref("");
const viaPreviewSiteID = ref("1");
const viaPreview = ref(null);

const advertisedRouteDetails = computed(() => {
  if (
    props.currentMachine.advertisedRouteDetails &&
    props.currentMachine.advertisedRouteDetails.length > 0
  ) {
    return props.currentMachine.advertisedRouteDetails;
  }
  return (props.currentMachine.advertisedIPs || []).map((prefix) => ({
    prefix,
    enabled: isAllowedRoute(prefix),
    isVia: false,
    displayLabel: prefix,
  }));
});

function isAllowedRoute(routeCIDR) {
  if (!props.currentMachine.allowedIPs || props.currentMachine.allowedIPs.length == 0) {
    return false;
  }
  for (var id in props.currentMachine.allowedIPs) {
    if (routeCIDR === props.currentMachine.allowedIPs[id]) {
      return true;
    }
  }
  return false;
}

onMounted(() => {});

watch(
  () => props.currentMachine.id,
  () => {
    viaPreview.value = null;
    viaPreviewPrefix.value = "";
    viaPreviewSiteID.value = "1";
  }
);

function routeDisplayHint(route) {
  if (!route || !route.isVia) {
    return "";
  }
  return `重叠站点: 原始网段 ${route.viaOriginalPrefix}，site ID ${route.viaSiteID}`;
}

function updateSubnet(type) {
  inputBlocking.value = true;
  var newAllowedIPs = [];
  if (type != "Off") {
    for (var i in props.currentMachine.advertisedIPs) {
      if (
        type == "On" ||
        document.getElementById(props.currentMachine.advertisedIPs[i]).checked
      ) {
        newAllowedIPs.push(props.currentMachine.advertisedIPs[i]);
      }
    }
  }
  var setExitNode = document.getElementById("exit-node").checked;
  axios
    .post("/admin/api/machines", {
      mid: props.id,
      state: "set-route-settings",
      allowedIPs: newAllowedIPs,
      allowedExitNode: setExitNode,
    })
    .then(function (response) {
      if (response.data["status"] == "success") {
        emit(
          "update-done",
          response.data["data"]["advertisedIPs"],
          response.data["data"]["allowedIPs"],
          response.data["data"]["extraIPs"],
          response.data["data"]["allowedExitNode"],
          response.data["data"]["advertisedRouteDetails"] || []
        );
      } else {
        emit("update-fail", response.data["status"].substring(6));
      }
    })
    .catch(function (error) {
      emit("update-fail", error);
    });
  inputBlocking.value = false;
}

function previewViaRoute() {
  viaPreview.value = null;
  axios
    .post("/admin/api/machines", {
      mid: props.id,
      state: "preview-via-route",
      prefix: viaPreviewPrefix.value,
      siteID: viaPreviewSiteID.value,
    })
    .then(function (response) {
      if (response.data["status"] == "success") {
        viaPreview.value = response.data["data"]["viaRoutePreview"];
      } else {
        emit("update-fail", response.data["status"].substring(6));
      }
    })
    .catch(function (error) {
      emit("update-fail", error);
    });
}
</script>

<template>
  <div
    @click.self="$emit('close')"
    class="fixed overflow-y-auto inset-0 py-8 z-30 bg-gray-900 bg-opacity-[0.07]"
    style="pointer-events: auto"
  >
    <div
      class="bg-white rounded-lg relative p-4 md:p-6 text-gray-700 max-w-lg min-w-[19rem] my-8 mx-auto w-[97%] shadow-2xl"
      style="pointer-events: auto"
    >
      <header class="flex items-center justify-between space-x-4 mb-5 mr-8">
        <div class="font-semibold text-lg truncate">
          编辑 {{ currentMachine.name }} 子网转发设置
        </div>
      </header>
      <div class="mb-6">
        <h3 class="font-semibold text-gray-800 mb-2">子网转发路由</h3>
        <p class="text-gray-700 mb-3">通过子网转发连接您无法安装蜃境客户端的设备</p>
        <div>
          <div
            v-if="!currentMachine.hasSubnets"
            class="rounded-md border bg-stone-100 border-stone-300 p-6"
          >
            <div class="flex justify-center">
              <div class="w-full text-center max-w-xl text-gray-500">
                该设备未披露任何子网转发路由
              </div>
            </div>
          </div>
          <div v-if="currentMachine.hasSubnets">
            <li
              v-for="route in advertisedRouteDetails"
              class="flex items-center py-2 border-t"
            >
              <div>
                <input
                  @change="updateSubnet('Update')"
                  :disabled="inputBlocking"
                  :checked="isAllowedRoute(route.prefix)"
                  :id="route.prefix"
                  type="checkbox"
                  class="toggle block mr-3"
                />
              </div>
              <div class="flex flex-col items-start">
                <label :for="route.prefix">{{ route.displayLabel || route.prefix }}</label>
                <span v-if="routeDisplayHint(route)" class="text-xs text-gray-500 mt-0.5">
                  {{ routeDisplayHint(route) }}
                </span>
              </div>
            </li>
          </div>
          <div
            v-if="currentMachine.hasSubnets"
            class="flex-1 flex border-t items-center pt-2"
          >
            <button
              @click="updateSubnet('Off')"
              class="btn border-0 bg-red-600 hover:bg-red-700 disabled:bg-red-600/60 disabled:text-white/60 text-white h-9 min-h-fit mr-2"
              :disabled="!hasAllowedSubnet"
            >
              全部禁用
            </button>
            <button
              @click="updateSubnet('On')"
              class="btn border border-stone-300 hover:border-stone-300 disabled:border-stone-300 bg-base-200 hover:bg-base-300 disabled:bg-base-200/60 text-black disabled:text-black/30 h-9 min-h-fit"
              :disabled="!hasExtraSubnet"
            >
              全部启用
            </button>
          </div>
        </div>
      </div>
      <div class="mt-8">
        <h3 class="font-semibold text-gray-800 mb-2">重叠子网（4via6）辅助</h3>
        <p class="text-gray-700 mb-3">
          对齐 Tailscale 官方 4via6 语义。不同站点如果都用了相同的 IPv4 子网，请为每个站点分配不同
          site ID；同一站点做双机高可用时，两台设备使用相同 site ID。
        </p>
        <div class="grid gap-3 md:grid-cols-[minmax(0,1fr)_9rem_auto]">
          <input
            v-model.trim="viaPreviewPrefix"
            type="text"
            autocomplete="off"
            placeholder="例如 192.168.1.0/24"
            class="input w-full border focus:outline-blue-500/60 hover:border border-stone-200 hover:border-stone-400 rounded-md h-9 min-h-fit"
          />
          <input
            v-model.trim="viaPreviewSiteID"
            type="number"
            min="1"
            autocomplete="off"
            placeholder="site ID"
            class="input w-full border focus:outline-blue-500/60 hover:border border-stone-200 hover:border-stone-400 rounded-md h-9 min-h-fit"
          />
          <button
            @click="previewViaRoute"
            class="btn border border-stone-300 hover:border-stone-300 bg-base-200 hover:bg-base-300 text-black h-9 min-h-fit"
            type="button"
          >
            生成 4via6
          </button>
        </div>
        <div v-if="viaPreview" class="mt-4 rounded-md border bg-stone-50 border-stone-200 p-4 text-sm">
          <div class="mb-1">
            <span class="font-medium">原始网段：</span>{{ viaPreview.originalPrefix }}
          </div>
          <div class="mb-1">
            <span class="font-medium">site ID：</span>{{ viaPreview.siteID }}
          </div>
          <div class="mb-1">
            <span class="font-medium">应广告的 4via6 前缀：</span>
            <span class="font-mono break-all">{{ viaPreview.viaPrefix }}</span>
          </div>
          <div>
            <span class="font-medium">设备配置命令：</span>
            <span class="font-mono break-all">{{ viaPreview.command }}</span>
          </div>
        </div>
      </div>
      <div>
        <h3 class="font-semibold flex items-center text-gray-800 mb-2">出口节点</h3>
        <p class="text-gray-700 mb-3">允许您的网络上访问互联网流量通过该设备流出</p>
        <div class="flex items-center">
          <input
            @change="updateSubnet('Update')"
            :disabled="inputBlocking || !currentMachine.advertisedExitNode"
            :checked="currentMachine.allowedExitNode"
            id="exit-node"
            type="checkbox"
            class="toggle mr-3"
          />
          <label for="exit-node">用作出口节点</label>
          <span
            v-if="!currentMachine.advertisedExitNode"
            class="tooltip"
            data-tip="该设备未声明它自己为出口节点。可使用 --advertise-exit-node 参数再次运行以开启。"
          >
            <svg
              xmlns="http://www.w3.org/2000/svg"
              width="1em"
              height="1em"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              stroke-width="2"
              stroke-linecap="round"
              stroke-linejoin="round"
              class="mx-2 text-gray-500"
            >
              <circle cx="12" cy="12" r="10"></circle>
              <line x1="12" y1="16" x2="12" y2="12"></line>
              <line x1="12" y1="8" x2="12.01" y2="8"></line>
            </svg>
          </span>
        </div>
      </div>

      <button
        @click="$emit('close')"
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

<style scoped>
.toggle {
  border: 0;
  --tglbg: #d6d3d1;
  background-color: white;
}

.toggle:checked {
  border: 0;
  --tglbg: #1e40af;
  background-color: white;
}

.toggle:disabled {
  --togglehandleborder: 0 0 0 3px white inset,
    var(--handleoffsetcalculator) 0 0 3px white inset;
}

.tooltip {
  --tooltip-color: #faf9f8;
  --tooltip-text-color: #3a3939;
  text-align: start;
  white-space: normal;
}

.tooltip:before {
  max-width: 16rem;
  font-size: small;
  font-weight: 300;
  border-radius: 0.375rem;
  box-shadow: 0 1px 3px 0 rgb(0 0 0 / 0.1), 0 1px 2px -1px rgb(0 0 0 / 0.1);
  padding-left: 0.75rem;
  padding-right: 0.75rem;
  padding-top: 0.5rem;
  padding-bottom: 0.5rem;
  border-width: 1px;
  border-color: #e1dfde;
}
</style>
