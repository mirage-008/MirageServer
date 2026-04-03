<script setup>
import { ref, computed, nextTick, onMounted, onUnmounted, watch, watchEffect } from "vue";
import { onBeforeRouteLeave, onBeforeRouteUpdate } from "vue-router";
import MachineMenu from "./MachineMenu.vue";
import RemoveMachine from "./mmenu/RemoveMachine.vue";
import UpdateHostname from "./mmenu/UpdateHostname.vue";
import UpdateAddresses from "./mmenu/UpdateAddresses.vue";
import SetSubnet from "./mmenu/SetSubnet.vue";
import Toast from "./Toast.vue";
import EditTags from "./mmenu/EditTags.vue";
import ShareMachine from "./mmenu/ShareMachine.vue";

//与框架交互部分

//界面控制部分
const activeBtn = ref(null);
const btnLeft = ref(0);
const btnTop = ref(0);
function refreshMachineMenuPos() {
  if (activeBtn.value != null) {
    btnLeft.value = activeBtn.value?.getBoundingClientRect().left + 14;
    btnTop.value = activeBtn.value?.getBoundingClientRect().top;
  }
}
function openMachineMenu(mID, event) {
  activeBtn.value = event.target;
  while (activeBtn.value?.tagName != "DIV" && activeBtn.value?.tagName != "div") {
    activeBtn.value = activeBtn.value.parentNode;
  }
  currentMID.value = mID;
  btnLeft.value = activeBtn.value?.getBoundingClientRect().left + 14;
  btnTop.value = activeBtn.value?.getBoundingClientRect().top;
  machineMenuShow.value = true;
}
function closeMachineMenu() {
  activeBtn.value = null;
  machineMenuShow.value = false;
}

const toastShow = ref(false);
const toastMsg = ref("");
watch(toastShow, () => {
  if (toastShow.value) {
    setTimeout(function () {
      toastShow.value = false;
    }, 5000);
  }
});

const currentMID = ref("-1");
function mouseOnMachine(mid) {
  currentMID.value = mid;
  machineBtnShow.value = true;
}
function mouseLeaveMachine() {
  machineBtnShow.value = false;
}
const machineIPShow = ref(false);
const machineMenuShow = ref(false);
const machineBtnShow = ref(false);

const delConfirmShow = ref(false);
function showDelConfirm() {
  machineBtnShow.value = false;
  closeMachineMenu();
  delConfirmShow.value = true;
}
const editTagsShow = ref(false);
function showEditTags() {
  machineBtnShow.value = false;
  closeMachineMenu();
  editTagsShow.value = true;
}

const updateHostnameShow = ref(false);
function showUpdateHostname() {
  machineBtnShow.value = false;
  closeMachineMenu();
  updateHostnameShow.value = true;
}
const updateAddressesShow = ref(false);
function showUpdateAddresses() {
  machineBtnShow.value = false;
  closeMachineMenu();
  updateAddressesShow.value = true;
}
const setSubnetShow = ref(false);
function showSetSubnet() {
  machineBtnShow.value = false;
  closeMachineMenu();
  setSubnetShow.value = true;
}
const shareMachineShow = ref(false);
function showShareMachine() {
  machineBtnShow.value = false;
  closeMachineMenu();
  shareMachineShow.value = true;
}

function machineActiveShares(id) {
  return MList.value[id]?.activeShares || [];
}

function shareCreatedDone(shareResponse) {
  const mid = currentMID.value;
  if (!MList.value[mid]) {
    return;
  }
  const share = shareResponse?.share || shareResponse;
  if (!share) {
    return;
  }

  const currentShares = machineActiveShares(mid).filter(function (item) {
    return item.id != share.id;
  });
  currentShares.push(share);
  MList.value[mid]["activeShares"] = currentShares;
  MList.value[mid]["issharedout"] = true;
  MList.value[mid]["shareID"] = currentShares[0]?.stableId || share.stableId || "";
  MList.value[mid]["acceptedShareCount"] = currentShares.filter(function (item) {
    return item.status == "accepted";
  }).length;
  toastMsg.value = "已创建设备分享！";
  toastShow.value = true;
}

function shareRevokedDone(share) {
  const mid = currentMID.value;
  if (!MList.value[mid]) {
    return;
  }

  const currentShares = machineActiveShares(mid).map(function (item) {
    if (item.id == share.id) {
      return {
        ...item,
        status: "revoked",
        revokedAt: new Date().toISOString(),
      };
    }
    return item;
  });
  MList.value[mid]["activeShares"] = currentShares;
  MList.value[mid]["issharedout"] = currentShares.length > 0;
  MList.value[mid]["shareID"] = currentShares[0]?.stableId || "";
  MList.value[mid]["acceptedShareCount"] = currentShares.filter(function (item) {
    return item.status == "accepted";
  }).length;
  toastMsg.value = "已撤销设备分享！";
  toastShow.value = true;
}

function openMachineMenuForItem(id, event) {
  openMachineMenu(id, event);
}

function externalReadonlyToast() {
  toastMsg.value = "外部共享设备仅支持查看";
  toastShow.value = true;
}

function openReadonlyMachineMenu(id, event) {
  openMachineMenu(id, event);
  if (MList.value[id]?.isExternal) {
    externalReadonlyToast();
  }
}

function openMachineAction(id, event) {
  if (MList.value[id]?.isExternal) {
    openReadonlyMachineMenu(id, event);
    return;
  }
  openMachineMenuForItem(id, event);
}

function actionMachineLabel(m) {
  return m?.isExternal ? "查看" : "操作";
}

function actionMachineButtonClass(m) {
  if (m?.isExternal) {
    return "py-0.5 px-2 shadow-none rounded-md border border-gray-300/100 bg-gray-50 hover:bg-gray-100 hover:border-gray-300/100 hover:shadow-md hover:cursor-pointer active:border-gray-300/100 active:shadow focus:outline-none focus:ring transition-shadow duration-100 ease-in-out z-20 text-gray-500 text-sm";
  }
  return "py-0.5 px-2 shadow-none rounded-md border border-gray-300/0 hover:border-gray-300/100 hover:bg-gray-100 hover:shadow-md hover:cursor-pointer active:border-gray-300/100 active:shadow focus:outline-none focus:ring transition-shadow duration-100 ease-in-out z-20";
}

function actionMachineButtonActiveClass(m) {
  if (m?.isExternal) {
    return "flex-none border button-outline bg-gray-50 shadow-md cursor-pointer focus:outline-none focus:ring -mt-0.5 relative py-0.5 px-2 rounded-md border-gray-300/100 hover:border-gray-300/100 hover:bg-gray-100 hover:shadow-md active:border-gray-300/100 transition-shadow duration-100 ease-in-out z-20 text-gray-500 text-sm";
  }
  return "flex-none w-12 border button-outline bg-white shadow-md cursor-pointer focus:outline-none focus:ring -mt-0.5 relative py-0.5 px-2 rounded-md border-gray-300/100 hover:border-gray-300/100 hover:bg-gray-100 hover:shadow-md hover:cursor-pointer active:border-gray-300/100 transition-shadow duration-100 ease-in-out z-20";
}

function actionMachineContainerClass(m) {
  return m?.isExternal ? "flex-none min-w-[3.5rem] -mt-0.5 relative" : "flex-none w-12 -mt-0.5 relative";
}

function showActionIcon(m) {
  return !m?.isExternal;
}

function machineShareBadgeText(m) {
  if (!m?.issharedout) {
    return "";
  }
  const accepted = m.acceptedShareCount || 0;
  const pending = pendingShareCount(m);
  const rejected = rejectedShareCount(m);
  if (accepted > 0) {
    return `对外共享+${accepted}`;
  }
  if (pending > 0) {
    return "待处理共享";
  }
  if (rejected > 0) {
    return "共享被拒";
  }
  return "共享记录";
}

function machineShareTooltip(m) {
  const shares = m?.activeShares || [];
  if (shares.length == 0) {
    return "";
  }
  return shares
    .map(function (share) {
      return `${share.targetIdentity}（${shareStatusText(share.status)}）`;
    })
    .join("\n");
}

function firstMachineShareLink(m) {
  return m?.activeShares?.[0]?.shareURL || "";
}

function copyShareLink(m) {
  const shareLink = firstMachineShareLink(m);
  if (!shareLink) {
    toastMsg.value = "暂无可复制的邀请链接";
    toastShow.value = true;
    return;
  }
  navigator.clipboard.writeText(shareLink).then(function () {
    toastMsg.value = "邀请链接已复制到粘贴板！";
    toastShow.value = true;
  });
}

function shareLinkButtonText(m) {
  return firstMachineShareLink(m) ? "复制链接" : "";
}

function pendingShareCount(m) {
  return (m?.activeShares || []).filter(function (share) {
    return share.status == "pending";
  }).length;
}

function rejectedShareCount(m) {
  return (m?.activeShares || []).filter(function (share) {
    return share.status == "rejected";
  }).length;
}

function machineShareDetailText(m) {
  if (!m?.issharedout) {
    return "";
  }
  const total = (m.activeShares || []).length;
  if (total == 0) {
    return "";
  }
  const pending = pendingShareCount(m);
  const detail = [];
  if (pending > 0) {
    detail.push(`${pending} 个待接受`);
  }
  if ((m.acceptedShareCount || 0) > 0) {
    detail.push(`${m.acceptedShareCount} 个已接受`);
  }
  const rejected = rejectedShareCount(m);
  if (rejected > 0) {
    detail.push(`${rejected} 个已拒绝`);
  }
  return detail.join(" · ");
}

function shareStatusText(status) {
  switch (status) {
    case "accepted":
      return "已接受";
    case "rejected":
      return "已拒绝";
    case "revoked":
      return "已撤销";
    default:
      return "待处理";
  }
}

function machineShareStatusClass(m) {
  return m?.isExternal ? "text-orange-600" : "text-gray-600";
}

//数据填充控制部分
const MList = ref({});
const tagOwners = ref([]);
const machinenumber = computed(() => {
  return Object.getOwnPropertyNames(MList.value).length;
});
let getMIntID;
function getMachines() {
  return new Promise((resolve, reject) => {
    axios
      .get("/admin/api/machines")
      .then(function (response) {
        if (
          response.data["needreauth"] != undefined ||
          response.data["needreauth"] == true
        ) {
          toastMsg.value =
            response.data["needreauthreason"] + "，登录状态失效，请重新登录";
          toastShow.value = true;
          reject();
          return;
        }
        if (response.data["status"] == "success") {
          let resList = response.data["data"]["machines"];
          for (var i in resList) {
            MList.value[resList[i].id] = resList[i];
            let expiresDuration =
              new Date(MList.value[resList[i].id]["expires"]).getTime() -
              new Date().getTime();
            if (expiresDuration < 1000 * 60 * 60 * 24 * 7 && expiresDuration > 0) {
              MList.value[resList[i].id]["soonexpiry"] = true;
            } else {
              MList.value[resList[i].id]["soonexpiry"] = false;
            }
          }
          resolve();
        } else {
          toastMsg.value = "获设备信息出错：" + response.data["status"].substring(6);
          toastShow.value = true;
          reject();
        }
      })
      .catch(function (error) {
        toastMsg.value = "更新页面出错：" + error;
        toastShow.value = true;
        reject();
      });
  });
}

function refreshTagOwners() {
  return axios
    .get("/admin/api/acls/tags")
    .then(function (response) {
      if (response.data["status"] == "success") {
        tagOwners.value = response.data["data"]["tagOwners"] || [];
        return tagOwners.value;
      }
      throw response.data["status"].substring(6);
    })
    .catch(function (error) {
      toastMsg.value = "获取标签失败：" + error;
      toastShow.value = true;
      throw error;
    });
}

onMounted(() => {
  refreshMachineMenuPos();
  window.addEventListener("resize", refreshMachineMenuPos);
  window.addEventListener("scroll", refreshMachineMenuPos);

  getMachines().then().catch();
  getMIntID = setInterval(() => {
    getMachines().then().catch();
  }, 15000);

  refreshTagOwners().catch(function () {});
});
onUnmounted(() => {
  window.removeEventListener("resize", refreshMachineMenuPos);
  window.removeEventListener("scroll", refreshMachineMenuPos);
});
onBeforeRouteLeave(() => {
  clearInterval(getMIntID);
});

//服务端请求
function setExpires(id) {
  closeMachineMenu();
  axios
    .post("/admin/api/machines", {
      mid: id,
      state: "set-expires",
    })
    .then(function (response) {
      if (response.data["status"] == "success") {
        MList.value[id]["neverExpires"] = response.data["data"]["neverExpires"];
        MList.value[id]["expirydesc"] = response.data["data"]["expires"];
        if (response.data["data"]["neverExpires"] == true) {
          toastMsg.value = "已禁用密钥过期";
        } else {
          toastMsg.value = "已启用密钥过期";
        }
        toastShow.value = true;
      } else {
        toastMsg.value = "失败：" + response.data["status"].substring(6);
        toastShow.value = true;
      }
    })
    .catch(function (error) {
      toastMsg.value = "失败：" + error;
      toastShow.value = true;
    });
}
function removeMachine(id) {
  axios
    .post("/admin/api/machine/remove", {
      mid: id,
    })
    .then(function (response) {
      if (response.data["status"] == "success") {
        delConfirmShow.value = false;
        toastMsg.value = MList.value[id]["name"] + "已从您的蜃境网络移除！";
        toastShow.value = true;
        delete MList.value[id];
      } else {
        toastMsg.value = "失败：" + response.data["status"].substring(6);
        toastShow.value = true;
      }
    })
    .catch(function (error) {
      toastMsg.value = "失败：" + error;
      toastShow.value = true;
    });
}

function hostnameUpdateDone(newName, newAutomaticNameMode, wantClose) {
  MList.value[currentMID.value]["name"] = newName;
  MList.value[currentMID.value]["automaticNameMode"] = newAutomaticNameMode;
  nextTick(() => {
    updateHostnameShow.value = !wantClose;
    nextTick(() => {
      toastMsg.value = "已更新设备名称！";
      toastShow.value = true;
    });
  });
}
function hostnameUpdateFail(msg) {
  toastMsg.value = "更新设备名称失败！";
  toastShow.value = true;
}

function addressesUpdateDone(newAddresses) {
  MList.value[currentMID.value]["addresses"] = newAddresses || [];
  updateAddressesShow.value = false;
  nextTick(() => {
    toastMsg.value = "已更新设备 IP！";
    toastShow.value = true;
  });
}
function addressesUpdateFail(msg) {
  toastMsg.value = "更新设备 IP 失败！" + msg;
  toastShow.value = true;
}

function tagsUpdateDone(mid, allowedTags, invalidTags) {
  MList.value[mid]["allowedTags"] = allowedTags;
  MList.value[mid]["invalidTags"] = invalidTags;
  MList.value[mid]["hasTags"] = allowedTags.length + invalidTags.length > 0;
  editTagsShow.value = false;
  nextTick(() => {
    nextTick(() => {
      toastMsg.value = "已更新设备标签！";
      toastShow.value = true;
    });
  });
}
function tagsUpdateFail(msg) {
  toastMsg.value = "更新设备标签失败！" + msg;
  toastShow.value = true;
}
function isInvalidTag(tag) {
  for (var i in tagOwners.value) {
    if (tagOwners.value[i].tagName == tag) {
      return false;
    }
  }
  return true;
}

function subnetUpdateDone(newAllIPs, newAllowedIPs, newExtraIPs, newEnExitNode, newRouteDetails) {
  MList.value[currentMID.value]["advertisedIPs"] = newAllIPs;
  MList.value[currentMID.value]["allowedIPs"] = newAllowedIPs;
  MList.value[currentMID.value]["extraIPs"] = newExtraIPs;
  MList.value[currentMID.value]["allowedExitNode"] = newEnExitNode;
  MList.value[currentMID.value]["advertisedRouteDetails"] = newRouteDetails || [];
  nextTick(() => {
    toastMsg.value = "已更新子网转发设置！";
    toastShow.value = true;
  });
}
function subnetUpdateFail(msg) {
  toastMsg.value = "更新子网转发设置失败！";
  toastShow.value = true;
}

function routeLabel(machine, routePrefix) {
  const details = machine["advertisedRouteDetails"] || [];
  const matched = details.find((route) => route.prefix === routePrefix);
  if (matched && matched.displayLabel) {
    return matched.displayLabel;
  }
  return routePrefix;
}

function routeHint(machine, routePrefix) {
  const details = machine["advertisedRouteDetails"] || [];
  const matched = details.find((route) => route.prefix === routePrefix);
  if (matched && matched.isVia) {
    return `重叠站点: 原始网段 ${matched.viaOriginalPrefix}，site ID ${matched.viaSiteID}`;
  }
  return "";
}

//客户端操作动作部分
function copyMIPv4() {
  navigator.clipboard
    .writeText(MList.value[currentMID.value]["addresses"][0])
    .then(function () {
      toastMsg.value = "蜃境网络IPv4地址已复制到粘贴板！";
      toastShow.value = true;
    });
}
function copyMIPv6() {
  navigator.clipboard
    .writeText(MList.value[currentMID.value]["addresses"][1])
    .then(function () {
      toastMsg.value = "蜃境网络IPv6地址已复制到粘贴板！";
      toastShow.value = true;
    });
}
</script>

<template>
  <main class="container mx-auto pb-20 md:pb-24">
    <section class="mb-24">
      <header class="mb-8">
        <div class="flex justify-between items-center">
          <div class="flex items-center">
            <h1 class="text-3xl font-semibold tracking-tight leading-tight mb-2">设备</h1>
          </div>
        </div>
      </header>

      <div
        class="inline-flex items-center align-middle justify-center font-medium border border-gray-200 bg-gray-200 text-gray-600 rounded-full px-2 py-1 leading-none text-sm mb-8"
      >
        {{ machinenumber }} 个设备
      </div>

      <div class="md:hidden space-y-3">
        <div
          v-for="(m, id) in MList"
          :key="'mobile-' + id"
          class="rounded-md border border-stone-200 bg-white p-4 shadow-sm"
        >
          <div class="flex items-start justify-between gap-3">
            <router-link class="min-w-0" :to="'/machines/' + m.addresses[0]">
              <div class="font-semibold text-gray-900 break-all">{{ m.name }}</div>
              <div class="mt-1 text-sm text-gray-600 break-all">{{ m.addresses[0] }}</div>
            </router-link>
            <div @click="openMachineAction(id, $event)" class="shrink-0">
              <button
                class="btn btn-sm border border-stone-300 bg-white hover:bg-stone-50 text-gray-700 h-8 min-h-fit"
                type="button"
              >
                {{ actionMachineLabel(m) }}
              </button>
            </div>
          </div>

          <div class="mt-2 text-sm text-gray-600 break-all">
            {{ m.os }} ·
            {{
              m.ipnVersion.split("-")[1].indexOf("t") != -1
                ? m.ipnVersion.split("-")[0]
                : m.ipnVersion
            }}
          </div>

          <div v-if="m.hasTags" class="mt-3 -mb-1">
            <span v-for="tag in m.allowedTags" :key="tag">
              <div
                class="inline-flex items-center align-middle justify-center font-medium border rounded-full px-2 py-1 leading-none text-xs mr-1 mb-1"
                :class="{
                  'border-gray-200 bg-gray-200 text-gray-600': isInvalidTag(tag),
                  'border-gray-300 bg-white': !isInvalidTag(tag),
                }"
              >
                <svg
                  v-if="isInvalidTag(tag)"
                  xmlns="http://www.w3.org/2000/svg"
                  width="10"
                  height="10"
                  viewBox="0 0 24 24"
                  fill="none"
                  stroke="currentColor"
                  stroke-width="3"
                  stroke-linecap="round"
                  stroke-linejoin="round"
                  class="mr-1 text-gray-500"
                >
                  <circle cx="12" cy="12" r="10"></circle>
                  <line x1="4.93" y1="4.93" x2="19.07" y2="19.07"></line>
                </svg>
                <span class="text-gray-500">{{ tag.substring(4) }}</span>
              </div>
            </span>
          </div>
          <div v-else class="mt-3 text-sm text-gray-600 break-all">{{ m.user }}</div>

          <div class="mt-3 flex flex-wrap gap-1">
            <span v-if="m.isExternal">
              <div
                class="inline-flex items-center align-middle justify-center font-medium border border-orange-50 bg-orange-50 text-orange-600 rounded-sm px-1 text-xs"
              >
                外部共享
              </div>
            </span>
            <span v-if="m.issharedout">
              <div
                class="inline-flex items-center align-middle justify-center font-medium border border-orange-50 bg-orange-50 text-orange-600 rounded-sm px-1 text-xs tooltip"
                :data-tip="machineShareTooltip(m)"
              >
                {{ machineShareBadgeText(m) }}
              </div>
            </span>
            <span v-if="m.expirydesc == '已过期'">
              <div
                class="inline-flex items-center align-middle justify-center font-medium border border-red-50 bg-red-50 text-red-600 rounded-sm px-1 text-xs"
              >
                已过期
              </div>
            </span>
            <span v-if="m.neverExpires">
              <div
                class="inline-flex items-center align-middle justify-center font-medium border border-gray-200 bg-gray-200 text-gray-600 rounded-sm px-1 text-xs"
              >
                永不过期
              </div>
            </span>
            <span v-if="m.soonexpiry">
              <div
                class="inline-flex items-center align-middle justify-center font-medium border border-gray-200 bg-gray-200 text-gray-600 rounded-sm px-1 text-xs"
              >
                {{ m.expirydesc }}
              </div>
            </span>
            <span v-if="m.hasSubnets">
              <div
                class="inline-flex items-center align-middle justify-center font-medium border border-blue-50 bg-blue-50 text-blue-600 rounded-sm px-1 text-xs"
              >
                子网转发
              </div>
            </span>
            <span v-if="m.advertisedExitNode">
              <div
                class="inline-flex items-center align-middle justify-center font-medium border border-blue-50 bg-blue-50 text-blue-600 rounded-sm px-1 text-xs"
              >
                出口节点
              </div>
            </span>
            <span v-if="m.isEphemeral">
              <div
                class="inline-flex items-center align-middle justify-center font-medium border border-blue-50 bg-blue-50 text-blue-600 rounded-sm px-1 text-xs"
              >
                自熄
              </div>
            </span>
          </div>

          <div class="mt-3 flex flex-wrap items-center gap-2 text-sm text-gray-600">
            <span>
              {{ m.connectedToControl ? "已连接" : m.lastSeen }}
            </span>
            <button
              v-if="m.issharedout && shareLinkButtonText(m)"
              @click.stop="copyShareLink(m)"
              class="inline-flex items-center align-middle justify-center font-medium border border-gray-200 bg-white text-gray-600 rounded-sm px-2 py-1 text-xs hover:bg-gray-100"
              type="button"
            >
              {{ shareLinkButtonText(m) }}
            </button>
            <span v-if="m.issharedout && machineShareDetailText(m)" class="text-xs text-gray-600">
              {{ machineShareDetailText(m) }}
            </span>
          </div>
        </div>
      </div>

      <table class="hidden md:table w-full">
        <thead>
          <tr>
            <th class="md:w-1/4 flex-auto md:flex-initial md:shrink-0 w-0 text-ellipsis">
              设备
            </th>
            <th class="hidden md:table-cell md:w-1/4">IP</th>
            <th class="hidden md:table-cell w-1/4 lg:w-1/5">版本</th>
            <th class="hidden lg:table-cell md:flex-auto">状态</th>
            <th class="table-cell justify-end ml-auto md:ml-0 relative w-1/6 lg:w-12">
              <span class="sr-only">设备操作菜单</span>
            </th>
          </tr>
        </thead>
        <tbody>
          <template v-for="(m, id) in MList">
            <tr
              :id="id"
              :v-if="MList[id] != nil"
              @mouseenter="mouseOnMachine(id)"
              @mouseleave="mouseLeaveMachine(id)"
              class="w-full px-0.5 hover"
            >
              <td
                class="md:w-1/4 flex-auto md:flex-initial md:shrink-0 w-0 text-ellipsis"
              >
                <router-link class="relative" :to="'/machines/' + m.addresses[0]">
                  <div class="items-center text-gray-900">
                    <p class="font-semibold hover:text-blue-500">
                      <span
                        :class="{
                          'bg-green-500': m.connectedToControl,
                          'bg-gray-300': !m.connectedToControl,
                        }"
                        class="inline-block w-2 h-2 rounded-full relative -top-px lg:hidden mr-2"
                      ></span>
                      <a class="stretched-link">{{ m.name }} </a>
                    </p>
                    <div class="md:hidden flex flex-wrap gap-x-1 text-sm text-gray-600 break-all">
                      <span class="text-sm">{{ m.addresses[0] }}</span
                      ><span>·</span
                      ><span
                        class="md:hidden text-gray-600 text-sm"
                        title="m.ipnVersion"
                        >{{ m.os }}</span
                      >
                    </div>
                  </div>
                  <div v-if="m.hasTags" class="mt-1">
                    <div class="-mt-1">
                      <span v-for="(tag, i) in m.allowedTags">
                        <div
                          class="inline-flex items-center align-middle justify-center font-medium border rounded-full px-2 py-1 leading-none text-xs mr-1 mt-1"
                          :class="{
                            'border-gray-200 bg-gray-200 text-gray-600': isInvalidTag(
                              tag
                            ),
                            'border-gray-300 bg-white': !isInvalidTag(tag),
                          }"
                        >
                          <svg
                            v-if="isInvalidTag(tag)"
                            xmlns="http://www.w3.org/2000/svg"
                            width="10"
                            height="10"
                            viewBox="0 0 24 24"
                            fill="none"
                            stroke="currentColor"
                            stroke-width="3"
                            stroke-linecap="round"
                            stroke-linejoin="round"
                            class="mr-1 text-gray-500"
                          >
                            <circle cx="12" cy="12" r="10"></circle>
                            <line x1="4.93" y1="4.93" x2="19.07" y2="19.07"></line>
                          </svg>
                          <span class="text-gray-500">{{ tag.substring(4) }}</span>
                        </div>
                      </span>
                    </div>
                  </div>
                  <div v-if="!m.hasTags">
                    <div class="flex items-center text-gray-600 text-sm">
                      <span>{{ m.user }} </span>
                    </div>
                  </div>
                </router-link>
                <div class="my-1">
                  <div>
                    <span v-if="m.isExternal">
                      <div
                        class="inline-flex items-center align-middle justify-center font-medium border border-orange-50 bg-orange-50 text-orange-600 rounded-sm px-1 text-xs mr-1"
                      >
                        外部共享
                      </div>
                    </span>
                    <span v-if="m.issharedout">
                      <div
                        class="inline-flex items-center align-middle justify-center font-medium border border-orange-50 bg-orange-50 text-orange-600 rounded-sm px-1 text-xs mr-1 tooltip"
                        :data-tip="machineShareTooltip(m)"
                      >
                        {{ machineShareBadgeText(m) }}
                      </div>
                    </span>
                    <span v-if="m.issharedout && shareLinkButtonText(m)">
                      <button
                        @click.stop="copyShareLink(m)"
                        class="inline-flex items-center align-middle justify-center font-medium border border-gray-200 bg-white text-gray-600 rounded-sm px-1 text-xs mr-1 hover:bg-gray-100"
                        type="button"
                      >
                        {{ shareLinkButtonText(m) }}
                      </button>
                    </span>
                    <span
                      v-if="m.issharedout && machineShareDetailText(m)"
                      :class="machineShareStatusClass(m)"
                      class="text-xs mr-1"
                    >
                      {{ machineShareDetailText(m) }}
                    </span>
                    <span
                      v-if="m.isExternal"
                      class="inline-flex items-center align-middle justify-center font-medium border border-gray-200 bg-gray-100 text-gray-500 rounded-sm px-1 text-xs mr-1"
                    >
                      只读
                    </span>
                    <span
                      v-if="m.isExternal && m.user"
                      class="text-xs text-gray-500 mr-1"
                    >
                      来源：{{ m.user }}
                    </span>
                    <span v-if="m.expirydesc == '已过期'">
                      <div
                        class="inline-flex items-center align-middle justify-center font-medium border border-red-50 bg-red-50 text-red-600 rounded-sm px-1 text-xs mr-1"
                      >
                        已过期
                      </div>
                    </span>
                    <span v-if="m.neverExpires">
                      <div
                        class="inline-flex items-center align-middle justify-center font-medium border border-gray-200 bg-gray-200 text-gray-600 rounded-sm px-1 text-xs mr-1"
                      >
                        永不过期
                      </div>
                    </span>
                    <span v-if="m.soonexpiry">
                      <div
                        class="inline-flex items-center align-middle justify-center font-medium border border-gray-200 bg-gray-200 text-gray-600 rounded-sm px-1 text-xs mr-1"
                      >
                        {{ m.expirydesc }}
                      </div>
                    </span>
                    <span v-if="m.hasSubnets">
                      <div
                        class="inline-flex items-center align-middle justify-center font-medium border border-blue-50 bg-blue-50 text-blue-600 rounded-sm px-1 text-xs mr-1"
                      >
                        子网转发
                        <div
                          v-if="m.hasSubnets && m.extraIPs && m.extraIPs.length > 0"
                          class="tooltip"
                          data-tip="该设备存在未批准子网转发，请在设备菜单的“编辑子网转发…”中检查"
                        >
                          <svg
                            xmlns="http://www.w3.org/2000/svg"
                            width="1em"
                            height="1em"
                            viewBox="0 0 24 24"
                            fill="none"
                            stroke="currentColor"
                            stroke-width="2.35"
                            stroke-linecap="round"
                            stroke-linejoin="round"
                            class="ml-1"
                          >
                            <circle cx="12" cy="12" r="10"></circle>
                            <line x1="12" y1="8" x2="12" y2="12"></line>
                            <line x1="12" y1="16" x2="12.01" y2="16"></line>
                          </svg>
                        </div>
                      </div>
                    </span>
                    <span v-if="m.advertisedExitNode">
                      <div
                        class="inline-flex items-center align-middle justify-center font-medium border border-blue-50 bg-blue-50 text-blue-600 rounded-sm px-1 text-xs mr-1"
                      >
                        出口节点
                        <div
                          v-if="!m.allowedExitNode"
                          class="tooltip"
                          data-tip="该设备申请被用作出口节点，请在设备菜单的“编辑子网转发…”中检查"
                        >
                          <svg
                            xmlns="http://www.w3.org/2000/svg"
                            width="1em"
                            height="1em"
                            viewBox="0 0 24 24"
                            fill="none"
                            stroke="currentColor"
                            stroke-width="2.35"
                            stroke-linecap="round"
                            stroke-linejoin="round"
                            class="ml-1"
                          >
                            <circle cx="12" cy="12" r="10"></circle>
                            <line x1="12" y1="8" x2="12" y2="12"></line>
                            <line x1="12" y1="16" x2="12.01" y2="16"></line>
                          </svg>
                        </div>
                      </div>
                    </span>
                    <span v-if="m.isEphemeral">
                      <div
                        class="inline-flex items-center align-middle justify-center font-medium border border-blue-50 bg-blue-50 text-blue-600 rounded-sm px-1 text-xs mr-1"
                      >
                        自熄
                      </div>
                    </span>
                  </div>
                </div>
              </td>
              <td class="hidden md:table-cell md:w-1/4">
                <ul>
                  <li class="font-medium pr-6">
                    <div
                      @mouseenter="machineIPShow = true"
                      @mouseleave="machineIPShow = false"
                      class="flex relative min-w-0"
                    >
                      <div class="truncate">
                        <span>{{ m.addresses[0] }} </span>
                      </div>
                      <div
                        v-if="machineIPShow && currentMID == id"
                        class="absolute -mt-1 -ml-2 -top-px -left-px shadow-md cursor-pointer rounded-md active:shadow-sm transition-shadow duration-100 ease-in-out z-20"
                        style="visibility: visible; max-width: 934px"
                      >
                        <div class="flex border rounded-md button-outline bg-white">
                          <div
                            @click="copyMIPv4"
                            class="flex min-w-0 py-1 px-2 hover:bg-gray-100 rounded-l-md"
                          >
                            <span class="inline-block select-none truncate"
                              ><span>
                                {{ m.addresses[0] }}
                              </span></span
                            ><span class="cursor-pointer text-blue-500 pl-2">复制</span>
                          </div>
                          <div
                            @click="copyMIPv6"
                            class="text-blue-500 py-1 px-2 border-l hover:bg-gray-100 rounded-r-md"
                          >
                            IPv6
                          </div>
                        </div>
                      </div>
                    </div>
                  </li>
                  <li v-for="allowedIP in m.allowedIPs">
                    <span>{{ routeLabel(m, allowedIP) }} </span>
                    <span v-if="routeHint(m, allowedIP)" class="text-xs text-gray-500 ml-1">
                      {{ routeHint(m, allowedIP) }}
                    </span>
                  </li>
                  <template v-for="extraIP in m.extraIPs">
                    <li class="tooltip text-gray-400" data-tip="这条子网转发未启用">
                      <span>{{ routeLabel(m, extraIP) }} </span>
                      <span v-if="routeHint(m, extraIP)" class="text-xs text-gray-500 ml-1">
                        {{ routeHint(m, extraIP) }}
                      </span>
                    </li>
                    <br />
                  </template>
                </ul>
              </td>
              <td class="hidden md:table-cell w-1/4 lg:w-1/5">
                <div class="flex items-center relative">
                  <div
                    v-if="m.availableUpdateVersion"
                    class="absolute top-0.5 -left-6 tooltip"
                    :data-tip="'可更新' + m.availableUpdateVersion"
                  >
                    <a
                      :href="'/downloads#/' + m.os"
                      target="_blank"
                      rel="noopener noreferrer"
                      ><svg
                        xmlns="http://www.w3.org/2000/svg"
                        width="1.125em"
                        height="1.125em"
                        viewBox="0 0 24 24"
                        fill="none"
                        stroke="currentColor"
                        stroke-width="2"
                        stroke-linecap="round"
                        stroke-linejoin="round"
                        class="text-gray-400"
                      >
                        <circle cx="12" cy="12" r="10"></circle>
                        <polyline points="16 12 12 8 8 12"></polyline>
                        <line x1="12" y1="16" x2="12" y2="8"></line></svg
                    ></a>
                  </div>
                  <div>
                    {{
                      m.ipnVersion.split("-")[1].indexOf("t") != -1
                        ? m.ipnVersion.split("-")[0]
                        : m.ipnVersion
                    }}
                  </div>
                </div>
                <div class="text-sm text-gray-600">
                  {{ m.os }}
                </div>
              </td>
              <td class="hidden lg:table-cell md:flex-auto">
                <span>
                  <div class="inline-flex items-center cursor-default">
                    <span
                      class="inline-block w-2 h-2 rounded-full mr-2"
                      :class="{
                        'bg-green-500': m.connectedToControl,
                        'bg-gray-300': !m.connectedToControl,
                      }"
                    ></span>
                    <span
                      v-if="m.connectedToControl"
                      class="text-sm text-gray-600 tooltip tooltip-top"
                      :data-tip="'最近在线于' + m.lastSeen"
                      >已连接</span
                    >
                    <span
                      v-else
                      class="text-sm text-gray-600 tooltip tooltip-top"
                      :data-tip="'最近在线于' + m.lastSeen"
                      >{{ m.lastSeen }}
                    </span>
                  </div>
                </span>
              </td>
              <td
                class="table-cell justify-end ml-auto md:ml-0 relative w-12 justify-items-end items-center md:items-start"
              >
                <div
                  v-if="(!machineBtnShow && !machineMenuShow) || currentMID != id"
                  @click="openMachineAction(id, $event)"
                  :class="actionMachineContainerClass(m)"
                >
                  <button :class="actionMachineButtonClass(m)">
                    <svg
                      v-if="showActionIcon(m)"
                      xmlns="http://www.w3.org/2000/svg"
                      width="24"
                      height="24"
                      viewBox="0 0 24 24"
                      fill="none"
                      stroke="currentColor"
                      stroke-width="2"
                      stroke-linecap="round"
                      stroke-linejoin="round"
                      class="text-gray-500"
                    >
                      <circle cx="12" cy="12" r="1"></circle>
                      <circle cx="19" cy="12" r="1"></circle>
                      <circle cx="5" cy="12" r="1"></circle>
                    </svg>
                    <span v-else>{{ actionMachineLabel(m) }}</span>
                  </button>
                </div>
                <!---->
                <div
                  v-if="(machineBtnShow || machineMenuShow) && currentMID == id"
                  @click="openMachineAction(id, $event)"
                  :class="actionMachineButtonActiveClass(m)"
                >
                  <svg
                    v-if="showActionIcon(m)"
                    xmlns="http://www.w3.org/2000/svg"
                    width="24"
                    height="24"
                    viewBox="0 0 24 24"
                    fill="none"
                    stroke="currentColor"
                    stroke-width="2"
                    stroke-linecap="round"
                    stroke-linejoin="round"
                    class="text-gray-500"
                  >
                    <circle cx="12" cy="12" r="1"></circle>
                    <circle cx="19" cy="12" r="1"></circle>
                    <circle cx="5" cy="12" r="1"></circle>
                  </svg>
                  <span v-else>{{ actionMachineLabel(m) }}</span>
                </div>
              </td>
            </tr>
          </template>
        </tbody>
      </table>
    </section>
  </main>

  <!-- 提示框显示 -->
  <Teleport to=".toast-container">
    <Toast :show="toastShow" :msg="toastMsg" @close="toastShow = false"></Toast>
  </Teleport>

  <!--设备配置菜单显示-->
  <Teleport to="body">
    <MachineMenu
      v-if="machineMenuShow"
      :toleft="btnLeft"
      :totop="btnTop"
      :neverExpires="MList[currentMID].neverExpires"
      :is-external="MList[currentMID].isExternal"
      @close="closeMachineMenu"
      @set-expires="setExpires(currentMID)"
      @showdialog-remove="showDelConfirm"
      @showdialog-edittags="showEditTags"
      @showdialog-updatehostname="showUpdateHostname"
      @showdialog-updateaddresses="showUpdateAddresses"
      @showdialog-setsubnet="showSetSubnet"
      @showdialog-share="showShareMachine"
    ></MachineMenu>

  </Teleport>

  <!-- 菜单弹出提示框显示 -->
  <Teleport to="body">
    <ShareMachine
      v-if="shareMachineShow"
      :id="currentMID"
      :machine-name="MList[currentMID].name"
      :shares="machineActiveShares(currentMID)"
      @close="shareMachineShow = false"
      @created="shareCreatedDone"
      @revoked="shareRevokedDone"
    ></ShareMachine>

    <!-- 删除设备提示框显示 -->
    <!-- 删除设备提示框显示 -->
    <RemoveMachine
      v-if="delConfirmShow"
      :machine-name="MList[currentMID].name"
      @close="delConfirmShow = false"
      @confirm="removeMachine(currentMID)"
    ></RemoveMachine>
    <!-- 修改设备名提示框显示 -->
    <UpdateHostname
      v-if="updateHostnameShow"
      :id="currentMID"
      :given-name="MList[currentMID].name"
      :host-name="MList[currentMID].hostname"
      :auto-gen="MList[currentMID].automaticNameMode"
      @close="updateHostnameShow = false"
      @update-done="hostnameUpdateDone"
      @update-fail="hostnameUpdateFail"
    >
    </UpdateHostname>
    <UpdateAddresses
      v-if="updateAddressesShow"
      :id="currentMID"
      :current-addresses="MList[currentMID].addresses"
      @close="updateAddressesShow = false"
      @update-done="addressesUpdateDone"
      @update-fail="addressesUpdateFail"
    ></UpdateAddresses>
    <!-- 设置子网转发提示框显示 -->
    <SetSubnet
      v-if="setSubnetShow"
      :id="currentMID"
      :current-machine="MList[currentMID]"
      @close="setSubnetShow = false"
      @update-done="subnetUpdateDone"
      @update-fail="subnetUpdateFail"
    ></SetSubnet>
    <!-- 修改设备名提示框显示 -->
    <EditTags
      v-if="editTagsShow"
      :id="currentMID"
      :current-machine="MList[currentMID]"
      :tag-owners="tagOwners"
      :given-name="MList[currentMID].name"
      :refresh-tag-owners="refreshTagOwners"
      @close="editTagsShow = false"
      @update-done="tagsUpdateDone"
      @update-fail="tagsUpdateFail"
    >
    </EditTags>
  </Teleport>
</template>

<style scoped>
.table tr.hover:hover th,
.table tr.hover:hover td,
.table tr.hover:nth-child(even):hover th,
.table tr.hover:nth-child(even):hover td {
  background-color: #faf9f8;
}

.table :where(thead, tfoot) :where(th, td) {
  background-color: #ffffff;
  color: #71706f;
  border-bottom-width: 1px;
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
