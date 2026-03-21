<script setup>
import { watch, ref, onMounted } from "vue";
import Toast from "../Toast.vue";
import SetGroup from "./SetGroup.vue";

const toastShow = ref(false);
const toastMsg = ref("");
watch(toastShow, () => {
  if (toastShow.value) {
    setTimeout(function () {
      toastShow.value = false;
    }, 5000);
  }
});

const groups = ref([]);
const orgUsers = ref([]);

function reloadGroups() {
  return axios
    .get("/admin/api/acls/groups")
    .then(function (response) {
      if (response.data["status"] == "success") {
        groups.value = response.data["data"]["groups"];
      } else {
        toastMsg.value = "获取用户组失败:" + response.data["status"].substring(6);
        toastShow.value = true;
      }
    })
    .catch(function (error) {
      toastMsg.value = "获取用户组失败:" + error;
      toastShow.value = true;
    });
}

function reloadUsers() {
  return axios
    .get("/admin/api/users")
    .then(function (response) {
      if (response.data["status"] == "success") {
        orgUsers.value = response.data["data"]["users"];
      } else {
        toastMsg.value = "获取用户失败:" + response.data["status"].substring(6);
        toastShow.value = true;
      }
    })
    .catch(function (error) {
      toastMsg.value = "获取用户失败:" + error;
      toastShow.value = true;
    });
}

function reloadData() {
  Promise.all([reloadGroups(), reloadUsers()]).then().catch();
}

onMounted(() => {
  reloadData();
});

const setGroupShow = ref(false);
const currentGroup = ref(null);

function showCreateGroup() {
  currentGroup.value = null;
  setGroupShow.value = true;
}

function showEditGroup(group) {
  currentGroup.value = JSON.parse(JSON.stringify(group));
  setGroupShow.value = true;
}

function saveGroupDone() {
  reloadGroups();
}

const wantRemoveGroup = ref("");
const deleteGroupShow = ref(false);

function toRemoveGroup(group) {
  wantRemoveGroup.value = group;
  deleteGroupShow.value = true;
}

function doRemoveGroup() {
  axios
    .delete("/admin/api/acls/groups/" + wantRemoveGroup.value, {})
    .then(function (response) {
      if (response.data["status"] == "success") {
        var tmpGroups = [];
        for (var i in groups.value) {
          if (groups.value[i].groupName != "group:" + response.data["data"]) {
            tmpGroups.push(groups.value[i]);
          }
        }
        groups.value = tmpGroups;
        deleteGroupShow.value = false;
      } else {
        toastMsg.value = "删除用户组失败:" + response.data["status"].substring(6);
        toastShow.value = true;
      }
    })
    .catch(function (error) {
      toastMsg.value = "删除用户组失败:" + error;
      toastShow.value = true;
    });
}
</script>

<template>
  <div class="flex-1">
    <div class="text-3xl font-semibold tracking-tight leading-tight mb-2 flex items-center">
      <h1 class="mr-2" tabindex="-1">用户组</h1>
    </div>
    <div class="text-gray-600 mt-3 mb-10">
      <p>查看和管理 ACL 中可复用的<strong>用户组</strong>(<strong>Groups</strong>)</p>
      <p class="mt-2">
        用户组可用于把多个用户聚合成一个 ACL 别名，后续在标签管理员或访问规则中复用。
      </p>
    </div>
    <div class="mt-10">
      <div class="flex justify-between items-center mt-16">
        <div>
          <h3 class="text-xl font-semibold tracking-tight">现有用户组</h3>
          <p class="text-gray-600">每个用户组名称都会以 <code>group:</code> 前缀保存到 ACL 策略中</p>
        </div>
        <button
          @click="showCreateGroup"
          class="btn border border-stone-300 hover:border-stone-300 disabled:border-stone-300 bg-base-200 hover:bg-base-300 disabled:bg-base-200/60 text-black disabled:text-black/30 h-9 min-h-fit ml-3 font-normal"
        >
          创建用户组…
        </button>
      </div>
      <div
        v-if="!groups || groups.length == 0"
        class="rounded-md border border-stone-200 mt-4 bg-stone-50 p-6"
      >
        <div class="flex justify-center">
          <div class="w-full text-center max-w-xl text-gray-500">你还没有任何用户组</div>
        </div>
      </div>

      <table v-if="groups && groups.length > 0" class="block border box-border rounded-lg mt-4 tb">
        <thead class="block font-semibold tracking-wider text-left text-xs text-stone-500">
          <tr class="flex border-b border-stone-200 pl-8 pr-4 lg:px-4">
            <th class="w-44 shrink-0 py-2">用户组名称</th>
            <th class="flex-1 shrink-0 py-2 min-w-0">成员</th>
            <th class="w-32 shrink-0 py-2 text-right">操作</th>
          </tr>
        </thead>
        <tbody class="block">
          <template v-for="(group, i) in groups" :key="group.groupName">
            <tr
              :class="{ 'border-t': i > 0 }"
              class="group flex border-stone-200 hover:bg-gray-50 pl-8 pr-4 lg:px-4 lg:cursor-auto border-b-0"
            >
              <td class="flex shrink-0 py-2 w-44">
                <pre class="text-sm truncate leading-6 font-semibold"><code>{{ group.groupName.substring(6) }}</code></pre>
              </td>
              <td class="flex-1 shrink-0 py-2 min-w-0">
                <div v-if="group.users.length == 0" class="text-sm text-gray-400">无成员</div>
                <span v-for="member in group.users" :key="member">
                  <div
                    class="inline-flex items-center align-middle justify-center font-medium border border-stone-200 bg-stone-200 text-stone-600 rounded-sm px-1 text-xs mr-1"
                  >
                    {{ member }}
                  </div>
                </span>
              </td>
              <td class="w-32 shrink-0 py-2 text-right">
                <button
                  @click="showEditGroup(group)"
                  type="button"
                  class="text-blue-500 hover:text-blue-700 mr-3"
                >
                  编辑…
                </button>
                <button
                  @click="toRemoveGroup(group.groupName.substring(6))"
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
    <SetGroup
      v-if="setGroupShow"
      :group="currentGroup"
      :users="orgUsers"
      @saved-group="saveGroupDone"
      @close="setGroupShow = false"
    ></SetGroup>
    <template v-if="deleteGroupShow">
      <div
        @click.self="deleteGroupShow = false"
        class="fixed overflow-y-auto inset-0 py-8 z-30 bg-gray-900 bg-opacity-[0.07]"
        style="pointer-events: auto"
      >
        <div
          class="bg-white rounded-lg relative p-4 md:p-6 text-gray-700 max-w-lg min-w-[19rem] my-8 mx-auto w-[97%] shadow-2xl"
          style="pointer-events: auto"
        >
          <header class="flex items-center justify-between space-x-4 mb-5 mr-8">
            <div class="font-semibold text-lg truncate">删除用户组</div>
          </header>
          <form @submit.prevent="doRemoveGroup">
            <p class="text-gray-700 mb-4">
              删除此用户组前请确保 ACL 规则或标签管理员中已不再引用它。
            </p>
            <footer class="flex mt-10 justify-end space-x-4">
              <button
                @click="deleteGroupShow = false"
                class="btn border border-base-300 hover:border-base-300 bg-base-200 hover:bg-base-300 text-black h-9 min-h-fit"
                type="button"
              >
                取消
              </button>
              <button
                class="btn border-0 bg-red-600 hover:bg-red-700 text-white h-9 min-h-fit"
                type="submit"
              >
                删除用户组
              </button>
            </footer>
          </form>
          <button
            @click="deleteGroupShow = false"
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
