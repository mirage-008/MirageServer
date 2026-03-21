<script setup>
import { watch, ref, onMounted } from "vue";
import Toast from "../Toast.vue";
import SetAutoApproverRoute from "./SetAutoApproverRoute.vue";

const toastShow = ref(false);
const toastMsg = ref("");
watch(toastShow, () => {
  if (toastShow.value) {
    setTimeout(function () {
      toastShow.value = false;
    }, 5000);
  }
});

const routes = ref([]);
const exitNodeApprovers = ref([]);
const exitNodeText = ref("");
const exitNodeInputBlocking = ref(false);

function parseApproverLines(text) {
  return text
    .split("\n")
    .map(function (line) {
      return line.trim();
    })
    .filter(function (line) {
      return line != "";
    });
}

function reloadAutoApprovers() {
  return axios
    .get("/admin/api/acls/auto-approvers")
    .then(function (response) {
      if (response.data["status"] == "success") {
        routes.value = response.data["data"]["routes"] || [];
        exitNodeApprovers.value = response.data["data"]["exitNode"] || [];
        exitNodeText.value = exitNodeApprovers.value.join("\n");
      } else {
        toastMsg.value = "获取自动审批配置失败:" + response.data["status"].substring(6);
        toastShow.value = true;
      }
    })
    .catch(function (error) {
      toastMsg.value = "获取自动审批配置失败:" + error;
      toastShow.value = true;
    });
}

onMounted(() => {
  reloadAutoApprovers();
});

const setRouteShow = ref(false);
const currentRoute = ref(null);

function showCreateRoute() {
  currentRoute.value = null;
  setRouteShow.value = true;
}

function showEditRoute(route) {
  currentRoute.value = JSON.parse(JSON.stringify(route));
  setRouteShow.value = true;
}

function saveRouteDone() {
  reloadAutoApprovers();
}

const wantRemoveRoute = ref(null);
const deleteRouteShow = ref(false);

function toRemoveRoute(route) {
  wantRemoveRoute.value = route;
  deleteRouteShow.value = true;
}

function doRemoveRoute() {
  axios
    .delete("/admin/api/acls/auto-approvers/routes/" + encodeURIComponent(wantRemoveRoute.value.route), {})
    .then(function (response) {
      if (response.data["status"] == "success") {
        deleteRouteShow.value = false;
        reloadAutoApprovers();
      } else {
        toastMsg.value = "删除自动审批路由失败:" + response.data["status"].substring(6);
        toastShow.value = true;
      }
    })
    .catch(function (error) {
      toastMsg.value = "删除自动审批路由失败:" + error;
      toastShow.value = true;
    });
}

function saveExitNodeApprovers() {
  exitNodeInputBlocking.value = true;
  axios
    .post("/admin/api/acls/auto-approvers/exit-node", {
      approvers: parseApproverLines(exitNodeText.value),
    })
    .then(function (response) {
      if (response.data["status"] == "success") {
        exitNodeApprovers.value = response.data["data"] || [];
        exitNodeText.value = exitNodeApprovers.value.join("\n");
      } else {
        toastMsg.value = "保存出口节点自动审批人失败:" + response.data["status"].substring(6);
        toastShow.value = true;
      }
    })
    .catch(function (error) {
      toastMsg.value = "保存出口节点自动审批人失败:" + error;
      toastShow.value = true;
    })
    .then(function () {
      exitNodeInputBlocking.value = false;
    });
}
</script>

<template>
  <div class="flex-1">
    <div class="text-3xl font-semibold tracking-tight leading-tight mb-2 flex items-center">
      <h1 class="mr-2" tabindex="-1">自动审批</h1>
    </div>
    <div class="text-gray-600 mt-3 mb-10">
      <p>查看和管理 ACL 中的<strong>自动审批配置</strong>(<strong>Auto Approvers</strong>)</p>
      <p class="mt-2">
        自动审批用于在满足审批人条件时，自动启用节点宣告的子网路由或出口节点能力。
      </p>
    </div>

    <div class="mt-10">
      <div class="flex justify-between items-center mt-16">
        <div>
          <h3 class="text-xl font-semibold tracking-tight">子网路由自动审批</h3>
          <p class="text-gray-600">
            为某个前缀指定审批人列表。常见审批人可填写用户、<code>group:xxx</code>、
            <code>tag:xxx</code> 或部分 <code>autogroup:*</code> 别名。
          </p>
        </div>
        <button
          @click="showCreateRoute"
          class="btn border border-stone-300 hover:border-stone-300 disabled:border-stone-300 bg-base-200 hover:bg-base-300 disabled:bg-base-200/60 text-black disabled:text-black/30 h-9 min-h-fit ml-3 font-normal"
        >
          添加路由…
        </button>
      </div>

      <div
        v-if="!routes || routes.length == 0"
        class="rounded-md border border-stone-200 mt-4 bg-stone-50 p-6"
      >
        <div class="flex justify-center">
          <div class="w-full text-center max-w-xl text-gray-500">当前还没有任何自动审批路由</div>
        </div>
      </div>

      <table v-if="routes && routes.length > 0" class="block border box-border rounded-lg mt-4 tb">
        <thead class="block font-semibold tracking-wider text-left text-xs text-stone-500">
          <tr class="flex border-b border-stone-200 pl-8 pr-4 lg:px-4">
            <th class="w-48 shrink-0 py-2">路由前缀</th>
            <th class="flex-1 shrink-0 py-2 min-w-0">审批人</th>
            <th class="w-32 shrink-0 py-2 text-right">操作</th>
          </tr>
        </thead>
        <tbody class="block">
          <template v-for="(route, i) in routes" :key="route.route">
            <tr
              :class="{ 'border-t': i > 0 }"
              class="group flex border-stone-200 hover:bg-gray-50 pl-8 pr-4 lg:px-4 lg:cursor-auto border-b-0"
            >
              <td class="w-48 shrink-0 py-2">
                <code class="text-sm">{{ route.route }}</code>
              </td>
              <td class="flex-1 shrink-0 py-2 min-w-0">
                <span v-for="approver in route.approvers" :key="approver">
                  <div
                    class="inline-flex items-center align-middle justify-center font-medium border border-stone-200 bg-stone-200 text-stone-600 rounded-sm px-1 text-xs mr-1 mb-1"
                  >
                    {{ approver }}
                  </div>
                </span>
              </td>
              <td class="w-32 shrink-0 py-2 text-right">
                <button
                  @click="showEditRoute(route)"
                  type="button"
                  class="text-blue-500 hover:text-blue-700 mr-3"
                >
                  编辑…
                </button>
                <button
                  @click="toRemoveRoute(route)"
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

    <div class="mt-16">
      <div>
        <h3 class="text-xl font-semibold tracking-tight">出口节点自动审批</h3>
        <p class="text-gray-600 mt-2">
          保存后，符合条件的节点在宣告出口节点能力时会自动通过审批。清空后保存即可关闭。
        </p>
      </div>
      <div class="rounded-lg border border-stone-200 mt-4 p-4 md:p-6 bg-white">
        <label for="exit-node-approvers" class="block font-medium mb-2">审批人</label>
        <textarea
          v-model="exitNodeText"
          id="exit-node-approvers"
          :disabled="exitNodeInputBlocking"
          class="textarea w-full border focus:outline-blue-500/60 hover:border disabled:hover:border-stone-200 disabled:border-stone-200 border-stone-200 hover:border-stone-400 rounded-md min-h-[10rem]"
          placeholder="每行一个审批人，例如&#10;alice&#10;group:netops&#10;tag:router"
        ></textarea>
        <div class="rounded-md border border-stone-200 bg-stone-50 p-4 mt-4 text-sm text-gray-600">
          <div class="font-medium text-gray-700 mb-2">当前已保存</div>
          <div v-if="exitNodeApprovers.length == 0" class="text-gray-500">当前没有配置任何出口节点自动审批人</div>
          <template v-else>
            <span v-for="approver in exitNodeApprovers" :key="approver">
              <div
                class="inline-flex items-center align-middle justify-center font-medium border border-stone-200 bg-stone-200 text-stone-600 rounded-sm px-1 text-xs mr-1 mb-1"
              >
                {{ approver }}
              </div>
            </span>
          </template>
        </div>
        <footer class="flex mt-6 justify-end space-x-4">
          <button
            :disabled="exitNodeInputBlocking"
            @click="exitNodeText = exitNodeApprovers.join('\n')"
            class="btn border border-base-300 hover:border-base-300 bg-base-200 hover:bg-base-300 text-black h-9 min-h-fit"
            type="button"
          >
            重置
          </button>
          <button
            :disabled="exitNodeInputBlocking"
            @click="saveExitNodeApprovers"
            class="btn border-0 bg-blue-500 hover:bg-blue-900 disabled:bg-blue-500/60 text-white disabled:text-white/60 h-9 min-h-fit"
            type="button"
          >
            保存
          </button>
        </footer>
      </div>
    </div>
  </div>

  <Teleport to="body">
    <SetAutoApproverRoute
      v-if="setRouteShow"
      :route="currentRoute"
      @saved-route="saveRouteDone"
      @close="setRouteShow = false"
    ></SetAutoApproverRoute>
    <template v-if="deleteRouteShow">
      <div
        @click.self="deleteRouteShow = false"
        class="fixed overflow-y-auto inset-0 py-8 z-30 bg-gray-900 bg-opacity-[0.07]"
        style="pointer-events: auto"
      >
        <div
          class="bg-white rounded-lg relative p-4 md:p-6 text-gray-700 max-w-lg min-w-[19rem] my-8 mx-auto w-[97%] shadow-2xl"
          style="pointer-events: auto"
        >
          <header class="flex items-center justify-between space-x-4 mb-5 mr-8">
            <div class="font-semibold text-lg truncate">删除自动审批路由</div>
          </header>
          <form @submit.prevent="doRemoveRoute">
            <p class="text-gray-700 mb-4">
              删除 <code>{{ wantRemoveRoute ? wantRemoveRoute.route : "" }}</code>
              的自动审批配置后，对应前缀将恢复为手动审批。
            </p>
            <footer class="flex mt-10 justify-end space-x-4">
              <button
                @click="deleteRouteShow = false"
                class="btn border border-base-300 hover:border-base-300 bg-base-200 hover:bg-base-300 text-black h-9 min-h-fit"
                type="button"
              >
                取消
              </button>
              <button
                class="btn border-0 bg-red-600 hover:bg-red-700 text-white h-9 min-h-fit"
                type="submit"
              >
                删除路由
              </button>
            </footer>
          </form>
          <button
            @click="deleteRouteShow = false"
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
