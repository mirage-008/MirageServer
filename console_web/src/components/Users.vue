<script setup>
import { ref, computed, nextTick, onMounted, onUnmounted, watch, watchEffect } from "vue";
import { onBeforeRouteLeave, onBeforeRouteUpdate } from "vue-router";
import UserMenu from "./UserMenu.vue";
import ChangeRole from "./umenu/ChangeRole.vue";
import RemoveUser from "./umenu/RemoveUser.vue";
import Toast from "./Toast.vue";
import axios from "axios";

//与框架交互部分

//界面控制部分
const activeBtn = ref(null);
const btnLeft = ref(0);
const btnTop = ref(0);
function refreshUserMenuPos() {
  if (activeBtn.value != null) {
    btnLeft.value = activeBtn.value?.getBoundingClientRect().left + 14;
    btnTop.value = activeBtn.value?.getBoundingClientRect().top;
  }
}
function openUserMenu(u, event) {
  activeBtn.value = event.target;
  while (activeBtn.value?.tagName != "DIV" && activeBtn.value?.tagName != "div") {
    activeBtn.value = activeBtn.value.parentNode;
  }
  selectUser.value = u;
  btnLeft.value = activeBtn.value?.getBoundingClientRect().left + 14;
  btnTop.value = activeBtn.value?.getBoundingClientRect().top;
  userMenuShow.value = true;
}
function closeUserMenu() {
  activeBtn.value = null;
  userMenuShow.value = false;
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

const currentUserId = ref(0);
const ownerId = ref(-1);
const pendingInvites = ref([]);
const externalUsers = ref([]);
const inviteTargetIdentity = ref("");
const inviteSubmitting = ref(false);
const canManageInvites = computed(() => {
  return ownerId.value > 0 && currentUserId.value == ownerId.value;
});
const pendingInviteCount = computed(() => {
  return pendingInvites.value.filter(function (invite) {
    return invite.status == "pending";
  }).length;
});

const selectUser = ref({});
function mouseOnUser(u) {
  selectUser.value = u;
  userBtnShow.value = true;
}
function mouseLeaveUser() {
  userBtnShow.value = false;
}

const userMenuShow = ref(false);
const userBtnShow = ref(false);

const changeRoleShow = ref(false);
function showChangeRole() {
  userBtnShow.value = false;
  closeUserMenu();
  changeRoleShow.value = true;
}

const targetUserMList = ref([]);
const removeUserShow = ref(false);
function showRemoveUser() {
  userBtnShow.value = false;
  closeUserMenu();
  getMachines().then(function (mlist) {
    targetUserMList.value = [];
    for (var i in mlist) {
      if (mlist[i]["user"] == selectUser.value["loginName"]) {
        targetUserMList.value = targetUserMList.value.concat(mlist[i]);
      }
    }
    removeUserShow.value = true;
  });
}

const wantedRoles = ref({});
function setWantedRole(newWantedRole) {
  wantedRoles.value[selectUser.value["id"]] = newWantedRole;
}

function doChangeRole() {
  const nextRole = wantedRoles.value[selectUser.value["id"]];
  const action = nextRole == "owner" ? "set_owner" : "set_member";
  axios
    .post("/admin/api/users", {
      userID: selectUser.value["id"],
      action: action,
    })
    .then(function (response) {
      if (response.data["status"] != "success") {
        toastMsg.value = response.data["status"].substring(6);
        toastShow.value = true;
      } else {
        let newRoleChn = "普通成员";
        switch (wantedRoles.value[selectUser.value["id"]]) {
          case "admin":
            newRoleChn = "管理员";
            break;
          case "owner":
            newRoleChn = "所有者";
            break;
        }
        changeRoleShow.value = false;
        toastMsg.value =
          "已修改 " + selectUser.value["loginName"] + " 角色为 " + newRoleChn;
        toastShow.value = true;
        if (newRoleChn != "所有者") {
          getUsers().then().catch();
        }
      }
    })
    .catch(function (error) {
      toastMsg.value = error;
      toastShow.value = true;
    });
}

function doRemoveUser() {
  axios
    .post("/admin/api/users", {
      userID: selectUser.value["id"],
      action: "delete_user",
    })
    .then(function (response) {
      if (response.data["status"] != "success") {
        toastMsg.value = response.data["status"].substring(6);
        toastShow.value = true;
      } else {
        removeUserShow.value = false;
        toastMsg.value = "已删除 " + selectUser.value["loginName"];
        toastShow.value = true;
        getUsers().then().catch();
      }
    })
    .catch(function (error) {
      toastMsg.value = error;
      toastShow.value = true;
    });
}

function createInvite() {
  const targetIdentity = inviteTargetIdentity.value.trim();
  if (!targetIdentity) {
    toastMsg.value = "请输入目标身份";
    toastShow.value = true;
    return;
  }

  inviteSubmitting.value = true;
  axios
    .post("/admin/api/users", {
      action: "create_invite",
      targetIdentity: targetIdentity,
    })
    .then(function (response) {
      if (response.data["status"] != "success") {
        toastMsg.value = response.data["status"].substring(6);
        toastShow.value = true;
        return;
      }

      const invite = response.data["data"]?.["invite"];
      if (invite) {
        pendingInvites.value = pendingInvites.value.filter(function (item) {
          return item.id != invite.id;
        });
        pendingInvites.value.unshift(invite);
      }
      inviteTargetIdentity.value = "";
      toastMsg.value = "已创建邀请";
      toastShow.value = true;
    })
    .catch(function (error) {
      toastMsg.value = String(error);
      toastShow.value = true;
    })
    .finally(function () {
      inviteSubmitting.value = false;
    });
}

function revokeInvite(invite) {
  if (!invite || inviteSubmitting.value) {
    return;
  }

  inviteSubmitting.value = true;
  axios
    .post("/admin/api/users", {
      action: "revoke_invite",
      inviteID: invite.id,
    })
    .then(function (response) {
      if (response.data["status"] != "success") {
        toastMsg.value = response.data["status"].substring(6);
        toastShow.value = true;
        return;
      }
      toastMsg.value = "已撤销邀请";
      toastShow.value = true;
      getUsers().then().catch();
    })
    .catch(function (error) {
      toastMsg.value = String(error);
      toastShow.value = true;
    })
    .finally(function () {
      inviteSubmitting.value = false;
    });
}

function formatInviteTime(value) {
  if (!value) {
    return "";
  }
  return new Date(value).toLocaleString();
}

function inviteStatusText(status) {
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

function inviteStatusClass(status) {
  switch (status) {
    case "accepted":
      return "border-green-100 bg-green-50 text-green-700";
    case "rejected":
      return "border-orange-100 bg-orange-50 text-orange-700";
    case "revoked":
      return "border-stone-200 bg-stone-100 text-stone-600";
    default:
      return "border-blue-100 bg-blue-50 text-blue-700";
  }
}

function inviteDecisionText(invite) {
  if (invite.acceptedAt) {
    return "接受于 " + formatInviteTime(invite.acceptedAt);
  }
  if (invite.rejectedAt) {
    return "拒绝于 " + formatInviteTime(invite.rejectedAt);
  }
  if (invite.revokedAt) {
    return "撤销于 " + formatInviteTime(invite.revokedAt);
  }
  return "";
}

function copyInviteLink(invite) {
  const inviteURL = invite?.inviteURL;
  if (!inviteURL) {
    toastMsg.value = "暂无可复制的邀请链接";
    toastShow.value = true;
    return;
  }

  navigator.clipboard.writeText(inviteURL).then(function () {
    toastMsg.value = "邀请链接已复制到粘贴板！";
    toastShow.value = true;
  });
}

//数据填充控制部分
const UserList = ref({});
const usersNum = computed(() => {
  return UserList.value.length;
});
let getUserIntID;
function getUsers() {
  return new Promise((resolve, reject) => {
    axios
      .get("/admin/api/users")
      .then(function (response) {
        if (response.data["status"] != "success") {
          toastMsg.value = "获用户信息出错：" + response.data["status"].substring(6);
          toastShow.value = true;
          reject();
        }

        // 处理成功情况
        currentUserId.value = response.data["data"]["currentUserID"];
        ownerId.value = response.data["data"]["ownerID"];
        UserList.value = response.data["data"]["users"];
        externalUsers.value = response.data["data"]["externalUsers"] || [];
        pendingInvites.value = response.data["data"]["pendingInvites"] || [];
        for (let i in UserList.value) {
          wantedRoles.value[UserList.value[i]["id"]] = wantedRoles.value[
            UserList.value[i]["id"]
          ]
            ? wantedRoles.value[UserList.value[i]["id"]]
            : UserList.value[i]["role"];
        }
        resolve();
      })
      .catch(function (error) {
        // 处理错误情况
        toastMsg.value = "获取用户信息出错：" + error;
        toastShow.value = true;
        reject();
      });
  });
}
onMounted(() => {
  refreshUserMenuPos();
  window.addEventListener("resize", refreshUserMenuPos);
  window.addEventListener("scroll", refreshUserMenuPos);

  getUsers().then().catch();
  getUserIntID = setInterval(() => {
    getUsers().then().catch();
  }, 15000);
});

onUnmounted(() => {
  window.removeEventListener("resize", refreshUserMenuPos);
  window.removeEventListener("scroll", refreshUserMenuPos);
});
onBeforeRouteLeave(() => {
  clearInterval(getUserIntID);
});

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
          resolve(response.data["data"]["machines"] || []);
        } else {
          toastMsg.value = "获取设备信息出错：" + response.data["status"].substring(6);
          toastShow.value = true;
          reject();
        }
      })
      .catch(function (error) {
        toastMsg.value = "获取设备信息出错：" + error;
        toastShow.value = true;
        reject();
      });
  });
}
</script>

<template>
  <main class="container mx-auto pb-20 md:pb-24">
    <section class="mb-24">
      <header class="mb-8">
        <div class="flex justify-between items-center">
          <div class="flex items-center">
            <h1 class="text-3xl font-semibold tracking-tight leading-tight mb-2">用户</h1>
          </div>
        </div>
        <p class="text-gray-600">管理你网络中的用户和他们的权限</p>
      </header>

      <div class="flex flex-wrap items-center gap-3 mb-8">
        <div
          class="inline-flex items-center align-middle justify-center font-medium border border-gray-200 bg-gray-200 text-gray-600 rounded-full px-2 py-1 leading-none text-sm"
        >
          {{ usersNum }} 个用户
        </div>
        <div
          v-if="externalUsers.length > 0"
          class="inline-flex items-center align-middle justify-center font-medium border border-orange-100 bg-orange-50 text-orange-600 rounded-full px-2 py-1 leading-none text-sm"
        >
          {{ externalUsers.length }} 个外部共享用户
        </div>
        <div
          v-if="pendingInvites.length > 0"
          class="inline-flex items-center align-middle justify-center font-medium border border-blue-100 bg-blue-50 text-blue-600 rounded-full px-2 py-1 leading-none text-sm"
        >
          {{ pendingInviteCount }} 个待处理邀请
        </div>
      </div>

      <section class="mb-8 rounded-md border border-gray-200 p-4 md:p-6">
        <header class="mb-4">
          <h2 class="text-lg font-semibold tracking-tight mb-1">邀请用户</h2>
          <p class="text-sm text-gray-600">
            输入目标身份后，系统会生成一条邀请链接，对方打开后可明确接受或拒绝。
          </p>
        </header>
        <form @submit.prevent="createInvite" class="flex flex-col gap-3 md:flex-row md:items-center">
          <input
            v-model="inviteTargetIdentity"
            :disabled="!canManageInvites || inviteSubmitting"
            class="input w-full border focus:outline-blue-500/60 hover:border disabled:hover:border-stone-200 disabled:border-stone-200 border-stone-200 hover:border-stone-400 rounded-md h-9 min-h-fit"
            type="text"
            placeholder="例如 user@example.com 或登录名"
          />
          <button
            :disabled="!canManageInvites || inviteSubmitting || inviteTargetIdentity.trim() == ''"
            class="btn border-0 bg-blue-500 hover:bg-blue-900 disabled:bg-blue-500/60 text-white disabled:text-white/60 h-9 min-h-fit"
            type="submit"
          >
            创建邀请
          </button>
        </form>
        <p v-if="!canManageInvites" class="text-sm text-gray-500 mt-3">仅所有者可以管理邀请。</p>

        <div class="mt-6">
          <div class="flex items-center justify-between mb-3">
            <h3 class="font-medium">邀请记录</h3>
            <span class="text-sm text-gray-500">{{ pendingInvites.length }} 条</span>
          </div>
          <div
            v-if="pendingInvites.length == 0"
            class="rounded-md border border-stone-200 bg-stone-50 p-5 text-center text-gray-500"
          >
            暂无邀请记录
          </div>
          <div v-else class="rounded-md border border-stone-200 divide-y divide-stone-200">
            <div
              v-for="invite in pendingInvites"
              :key="invite.id"
              class="p-4 flex flex-col gap-3 md:flex-row md:items-center md:justify-between"
            >
              <div class="min-w-0">
                <div class="font-medium break-all">{{ invite.targetIdentity }}</div>
                <div class="flex flex-wrap items-center gap-2 text-sm text-gray-600 mt-1">
                  <span
                    class="inline-flex items-center align-middle justify-center font-medium border rounded-sm px-2 py-0.5 text-xs"
                    :class="inviteStatusClass(invite.status)"
                  >
                    {{ inviteStatusText(invite.status) }}
                  </span>
                  <span v-if="invite.created"> · 创建于 {{ formatInviteTime(invite.created) }}</span>
                  <span v-if="inviteDecisionText(invite)"> · {{ inviteDecisionText(invite) }}</span>
                </div>
                <div v-if="invite.inviteURL" class="text-xs text-gray-500 mt-2 break-all">
                  链接：{{ invite.inviteURL }}
                </div>
              </div>
              <div class="flex shrink-0 justify-end gap-2">
                <button
                  v-if="invite.inviteURL"
                  @click="copyInviteLink(invite)"
                  class="btn border border-stone-200 bg-white hover:bg-stone-100 text-black h-9 min-h-fit"
                  type="button"
                >
                  复制链接
                </button>
                <button
                  v-if="invite.status == 'pending'"
                  :disabled="!canManageInvites || inviteSubmitting"
                  @click="revokeInvite(invite)"
                  class="btn border-0 bg-red-600 hover:bg-red-700 disabled:bg-red-600/60 text-white h-9 min-h-fit"
                  type="button"
                >
                  撤销
                </button>
              </div>
            </div>
          </div>
        </div>
      </section>

      <section v-if="externalUsers.length > 0" class="mb-8 rounded-md border border-orange-100 bg-orange-50/40 p-4 md:p-6">
        <header class="mb-4">
          <h2 class="text-lg font-semibold tracking-tight mb-1">外部共享用户</h2>
          <p class="text-sm text-gray-600">这些用户因外部共享设备而出现在你的可见范围内。</p>
        </header>
        <div class="grid grid-cols-1 md:grid-cols-2 gap-3">
          <div
            v-for="user in externalUsers"
            :key="user.id"
            class="rounded-md border border-orange-100 bg-white p-4"
          >
            <div class="font-medium break-all">{{ user.displayName }}</div>
            <div class="text-sm text-gray-600 mt-1 break-all">{{ user.loginName }}</div>
            <div class="text-xs text-gray-500 mt-2">来源组织：{{ user.domainName }}</div>
          </div>
        </div>
      </section>

      <div class="md:hidden space-y-3">
        <div
          v-for="u in UserList"
          :key="'mobile-' + u.id"
          class="rounded-md border border-stone-200 bg-white p-4 shadow-sm"
        >
          <div class="flex items-start justify-between gap-3">
            <div class="flex min-w-0 items-start gap-3">
              <div class="relative shrink-0 rounded-full overflow-hidden w-10 h-10 text-base">
                <div
                  class="flex items-center justify-center text-center capitalize text-white font-medium pointer-events-none w-10 h-10 text-base"
                  style="background-color: rgb(161, 56, 33)"
                >
                  {{ u.displayName[0] }}
                </div>
              </div>
              <div class="min-w-0">
                <div class="font-semibold text-gray-900 break-all">{{ u.displayName }}</div>
                <div class="mt-1 text-sm text-gray-600 break-all">{{ u.loginName }}</div>
              </div>
            </div>
            <div @click="openUserMenu(u, $event)" class="shrink-0">
              <button
                class="btn btn-sm border border-stone-300 bg-white hover:bg-stone-50 text-gray-700 h-8 min-h-fit"
                type="button"
              >
                操作
              </button>
            </div>
          </div>

          <div class="mt-3 flex flex-wrap gap-1">
            <span
              class="inline-flex items-center align-middle justify-center font-medium border border-gray-200 bg-gray-100 text-gray-600 rounded-sm px-1 text-xs"
            >
              {{ u.role == "owner" ? "所有者" : "普通成员" }}
            </span>
            <span v-if="u.status == 'suspend'">
              <div
                class="inline-flex items-center align-middle justify-center font-medium border border-red-50 bg-red-50 text-red-600 rounded-sm px-1 text-xs"
              >
                已冻结
              </div>
            </span>
          </div>

          <div class="mt-3 space-y-1 text-sm text-gray-600">
            <div>
              加入日期：
              {{
                new Date(u.created)
                  .toLocaleDateString()
                  .replace("/", "年")
                  .replace("/", "月") + "日"
              }}
            </div>
            <div>
              最近连线：
              {{
                u.currentlyConnected
                  ? "已连接"
                  : new Date(u.lastSeen)
                      .toLocaleString()
                      .replace("/", "年")
                      .replace("/", "月")
                      .replace(" ", "日 ")
              }}
            </div>
          </div>
        </div>
      </div>

      <table class="hidden md:table w-full">
        <thead>
          <tr>
            <th class="flex-auto table-cell items-center">用户</th>
            <th class="table-cell items-center md:w-1/4 lg:w-1/5">角色</th>
            <th class="hidden lg:table-cell items-center lg:w-1/5">加入日期</th>
            <th class="hidden lg:table-cell items-center lg:w-1/5">最近连线</th>
            <th class="table-cell justify-end ml-auto md:ml-0 relative items-center w-8">
              <span class="sr-only">用户操作菜单</span>
            </th>
          </tr>
        </thead>
        <tbody>
          <template v-for="(u, id) in UserList">
            <tr
              :id="id"
              :v-if="u != nil"
              @mouseenter="mouseOnUser(u)"
              @mouseleave="mouseLeaveUser()"
              class="w-full px-0.5 hover"
            >
              <td class="flex-auto flex items-center">
                <div
                  class="relative shrink-0 rounded-full overflow-hidden transition-all duration-300 w-8 h-8 md:w-12 md:h-12 md:text-xl mr-3"
                >
                  <div
                    class="flex items-center justify-center text-center capitalize text-white font-medium pointer-events-none transition-all duration-300 w-8 h-8 md:w-12 md:h-12 md:text-xl"
                    style="background-color: rgb(161, 56, 33)"
                  >
                    {{ u.displayName[0] }}
                  </div>
                </div>
                <router-link class="relative" :to="'/machines?q=' + u.loginName">
                  <div class="items-center text-gray-900">
                    <p class="font-semibold hover:text-blue-500">
                      <a class="stretched-link">{{ u.displayName }} </a>
                    </p>
                    <span v-if="u.status == 'suspend'">
                      <div
                        class="inline-flex items-center align-middle justify-center font-medium border border-red-50 bg-red-50 text-red-600 rounded-sm px-1 text-xs mr-1"
                      >
                        已冻结
                      </div>
                    </span>
                  </div>
                  <div class="flex items-center text-gray-600 text-sm">
                    <span>{{ u.loginName }} </span>
                  </div>
                </router-link>
              </td>
              <td class="table-cell items-center md:w-1/4 lg:w-1/5">
                <div class="flex relative min-w-0">
                  <div class="truncate">
                    <span>{{ u.role == "owner" ? "所有者" : "普通成员" }} </span>
                  </div>
                </div>
              </td>
              <td class="hidden lg:table-cell items-center lg:w-1/5">
                <time :datetime="u.created" :title="u.created">{{
                  new Date(u.created)
                    .toLocaleDateString()
                    .replace("/", "年")
                    .replace("/", "月") + "日"
                }}</time>
              </td>
              <td class="hidden lg:table-cell items-center lg:w-1/5">
                <span>
                  <div class="inline-flex items-center cursor-default">
                    <span
                      class="inline-block w-2 h-2 rounded-full mr-2"
                      :class="{
                        'bg-green-500': u.currentlyConnected,
                        'bg-gray-300': !u.currentlyConnected,
                      }"
                    ></span>
                    <span
                      v-if="u.currentlyConnected"
                      class="text-sm text-gray-600 tooltip tooltip-top"
                      :data-tip="
                        '最近在线于' +
                        new Date(u.lastSeen)
                          .toLocaleString()
                          .replace('/', '年')
                          .replace('/', '月')
                          .replace(' ', '日 ')
                      "
                      >已连接</span
                    >
                    <span
                      v-else
                      class="text-sm text-gray-600 tooltip tooltip-top"
                      :data-tip="
                        '最近在线于' +
                        new Date(u.lastSeen)
                          .toLocaleString()
                          .replace('/', '年')
                          .replace('/', '月')
                          .replace(' ', '日 ')
                      "
                      >{{
                        new Date(u.lastSeen)
                          .toLocaleString()
                          .replace("/", "年")
                          .replace("/", "月")
                          .replace(" ", "日 ")
                      }}
                    </span>
                  </div>
                </span>
              </td>
              <td
                class="table-cell justify-end ml-auto md:ml-0 relative items-center w-8"
              >
                <div
                  v-if="(!userBtnShow && !userMenuShow) || selectUser.id != u.id"
                  @click="openUserMenu(u, $event)"
                  class="flex-none w-12 -mt-0.5 relative"
                >
                  <button
                    class="py-0.5 px-2 shadow-none rounded-md border border-gray-300/0 hover:border-gray-300/100 hover:bg-gray-100 hover:shadow-md hover:cursor-pointer active:border-gray-300/100 active:shadow focus:outline-none focus:ring transition-shadow duration-100 ease-in-out z-20"
                  >
                    <svg
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
                  </button>
                </div>
                <!---->
                <div
                  v-if="(userBtnShow || userMenuShow) && selectUser.id == u.id"
                  @click="openUserMenu(u, $event)"
                  class="flex-none w-12 border button-outline bg-white shadow-md cursor-pointer focus:outline-none focus:ring -mt-0.5 relative py-0.5 px-2 rounded-md border-gray-300/100 hover:border-gray-300/100 hover:bg-gray-100 hover:shadow-md hover:cursor-pointer active:border-gray-300/100 transition-shadow duration-100 ease-in-out z-20"
                >
                  <svg
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
    <UserMenu
      v-if="userMenuShow"
      :toleft="btnLeft"
      :totop="btnTop"
      :login-name="selectUser.loginName"
      :cant-edit="selectUser.isOwner || selectUser.id == currentUserId"
      @close="closeUserMenu"
      @showdialog-changerole="showChangeRole"
      @showdialog-removeuser="showRemoveUser"
    ></UserMenu>
  </Teleport>

  <!-- 菜单弹出提示框显示 -->
  <Teleport to="body">
    <!-- 修改角色提示框显示 -->
    <ChangeRole
      v-if="changeRoleShow"
      :select-user="selectUser"
      :wanted-role="wantedRoles[selectUser.id]"
      :can-assign-owner="currentUserId == ownerId"
      @close="changeRoleShow = false"
      @set-wantedrole="setWantedRole"
      @change-role="doChangeRole"
    ></ChangeRole>
    <!-- 移除用户提示框显示 -->
    <RemoveUser
      v-if="removeUserShow"
      :select-user="selectUser"
      :user-machine-list="targetUserMList"
      @close="removeUserShow = false"
      @confirm-remove="doRemoveUser"
    >
    </RemoveUser>
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
