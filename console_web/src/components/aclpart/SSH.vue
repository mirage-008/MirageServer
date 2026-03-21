<script setup>
import { watch, ref, onMounted } from "vue";
import Toast from "../Toast.vue";
import SetSSH from "./SetSSH.vue";

const toastShow = ref(false);
const toastMsg = ref("");
watch(toastShow, () => {
  if (toastShow.value) {
    setTimeout(function () {
      toastShow.value = false;
    }, 5000);
  }
});

const rules = ref([]);

function reloadSSHRules() {
  return axios
    .get("/admin/api/acls/ssh")
    .then(function (response) {
      if (response.data["status"] == "success") {
        rules.value = response.data["data"]["rules"] || [];
      } else {
        toastMsg.value = "获取 SSH 规则失败:" + response.data["status"].substring(6);
        toastShow.value = true;
      }
    })
    .catch(function (error) {
      toastMsg.value = "获取 SSH 规则失败:" + error;
      toastShow.value = true;
    });
}

onMounted(() => {
  reloadSSHRules();
});

const setSSHShow = ref(false);
const currentRule = ref(null);

function showCreateRule() {
  currentRule.value = null;
  setSSHShow.value = true;
}

function showEditRule(rule) {
  currentRule.value = JSON.parse(JSON.stringify(rule));
  setSSHShow.value = true;
}

function saveRuleDone() {
  reloadSSHRules();
}

const wantRemoveRule = ref(null);
const deleteRuleShow = ref(false);

function toRemoveRule(rule) {
  wantRemoveRule.value = rule;
  deleteRuleShow.value = true;
}

function doRemoveRule() {
  axios
    .delete("/admin/api/acls/ssh/" + wantRemoveRule.value.id, {})
    .then(function (response) {
      if (response.data["status"] == "success") {
        deleteRuleShow.value = false;
        reloadSSHRules();
      } else {
        toastMsg.value = "删除 SSH 规则失败:" + response.data["status"].substring(6);
        toastShow.value = true;
      }
    })
    .catch(function (error) {
      toastMsg.value = "删除 SSH 规则失败:" + error;
      toastShow.value = true;
    });
}

function displayAction(rule) {
  if (rule.action == "check" && rule.checkPeriod) {
    return "check (" + rule.checkPeriod + ")";
  }
  return rule.action;
}
</script>

<template>
  <div class="flex-1">
    <div class="text-3xl font-semibold tracking-tight leading-tight mb-2 flex items-center">
      <h1 class="mr-2" tabindex="-1">SSH</h1>
    </div>
    <div class="text-gray-600 mt-3 mb-10">
      <p>查看和管理组织的<strong>SSH 访问规则</strong></p>
      <p class="mt-2">
        每条规则会定义来源 <code>src</code>、目标 <code>dst</code>、允许登录的本地用户
        <code>users</code> 以及动作 <code>accept</code> 或 <code>check</code>。
      </p>
    </div>

    <div class="mt-10">
      <div class="flex justify-between items-center mt-16">
        <div>
          <h3 class="text-xl font-semibold tracking-tight">现有 SSH 规则</h3>
          <p class="text-gray-600">
            保存前会按当前组织的 ACL 别名和 SSH 语义做校验，目标 <code>dst</code>
            会按机器实际匹配生效。
          </p>
        </div>
        <button
          @click="showCreateRule"
          class="btn border border-stone-300 hover:border-stone-300 disabled:border-stone-300 bg-base-200 hover:bg-base-300 disabled:bg-base-200/60 text-black disabled:text-black/30 h-9 min-h-fit ml-3 font-normal"
        >
          创建规则…
        </button>
      </div>

      <div
        v-if="!rules || rules.length == 0"
        class="rounded-md border border-stone-200 mt-4 bg-stone-50 p-6"
      >
        <div class="flex justify-center">
          <div class="w-full text-center max-w-xl text-gray-500">当前还没有任何 SSH 规则</div>
        </div>
      </div>

      <table v-if="rules && rules.length > 0" class="block border box-border rounded-lg mt-4 tb">
        <thead class="block font-semibold tracking-wider text-left text-xs text-stone-500">
          <tr class="flex border-b border-stone-200 pl-8 pr-4 lg:px-4">
            <th class="w-20 shrink-0 py-2">序号</th>
            <th class="w-40 shrink-0 py-2">动作</th>
            <th class="flex-1 shrink-0 py-2 min-w-0">来源</th>
            <th class="flex-1 shrink-0 py-2 min-w-0">目标</th>
            <th class="w-56 shrink-0 py-2">SSH 用户</th>
            <th class="w-32 shrink-0 py-2 text-right">操作</th>
          </tr>
        </thead>
        <tbody class="block">
          <template v-for="(rule, i) in rules" :key="rule.id">
            <tr
              :class="{ 'border-t': i > 0 }"
              class="group flex border-stone-200 hover:bg-gray-50 pl-8 pr-4 lg:px-4 lg:cursor-auto border-b-0"
            >
              <td class="w-20 shrink-0 py-2">
                <code class="text-sm">#{{ rule.id + 1 }}</code>
              </td>
              <td class="w-40 shrink-0 py-2">
                <code class="text-sm">{{ displayAction(rule) }}</code>
              </td>
              <td class="flex-1 shrink-0 py-2 min-w-0">
                <span v-for="src in rule.src" :key="src">
                  <div
                    class="inline-flex items-center align-middle justify-center font-medium border border-stone-200 bg-stone-200 text-stone-600 rounded-sm px-1 text-xs mr-1 mb-1"
                  >
                    {{ src }}
                  </div>
                </span>
              </td>
              <td class="flex-1 shrink-0 py-2 min-w-0">
                <span v-for="dst in rule.dst" :key="dst">
                  <div
                    class="inline-flex items-center align-middle justify-center font-medium border border-stone-200 bg-stone-200 text-stone-600 rounded-sm px-1 text-xs mr-1 mb-1"
                  >
                    {{ dst }}
                  </div>
                </span>
              </td>
              <td class="w-56 shrink-0 py-2">
                <span v-for="user in rule.users" :key="user">
                  <div
                    class="inline-flex items-center align-middle justify-center font-medium border border-stone-200 bg-stone-200 text-stone-600 rounded-sm px-1 text-xs mr-1 mb-1"
                  >
                    {{ user }}
                  </div>
                </span>
              </td>
              <td class="w-32 shrink-0 py-2 text-right">
                <button
                  @click="showEditRule(rule)"
                  type="button"
                  class="text-blue-500 hover:text-blue-700 mr-3"
                >
                  编辑…
                </button>
                <button
                  @click="toRemoveRule(rule)"
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
    <SetSSH
      v-if="setSSHShow"
      :rule="currentRule"
      @saved-rule="saveRuleDone"
      @close="setSSHShow = false"
    ></SetSSH>
    <template v-if="deleteRuleShow">
      <div
        @click.self="deleteRuleShow = false"
        class="fixed overflow-y-auto inset-0 py-8 z-30 bg-gray-900 bg-opacity-[0.07]"
        style="pointer-events: auto"
      >
        <div
          class="bg-white rounded-lg relative p-4 md:p-6 text-gray-700 max-w-lg min-w-[19rem] my-8 mx-auto w-[97%] shadow-2xl"
          style="pointer-events: auto"
        >
          <header class="flex items-center justify-between space-x-4 mb-5 mr-8">
            <div class="font-semibold text-lg truncate">删除 SSH 规则</div>
          </header>
          <form @submit.prevent="doRemoveRule">
            <p class="text-gray-700 mb-4">
              删除后，命中这条 SSH 规则的来源将不再按当前配置访问对应目标机器。
            </p>
            <footer class="flex mt-10 justify-end space-x-4">
              <button
                @click="deleteRuleShow = false"
                class="btn border border-base-300 hover:border-base-300 bg-base-200 hover:bg-base-300 text-black h-9 min-h-fit"
                type="button"
              >
                取消
              </button>
              <button
                class="btn border-0 bg-red-600 hover:bg-red-700 text-white h-9 min-h-fit"
                type="submit"
              >
                删除规则
              </button>
            </footer>
          </form>
          <button
            @click="deleteRuleShow = false"
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
