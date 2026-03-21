<script setup>
import { watch, ref, computed, onMounted } from "vue";
import Toast from "../Toast.vue";
import { useDisScroll } from "/src/utils.js";

const emit = defineEmits(["saved-route", "close"]);

const props = defineProps({
  route: Object,
});

useDisScroll();

const toastShow = ref(false);
const toastMsg = ref("");
watch(toastShow, () => {
  if (toastShow.value) {
    setTimeout(function () {
      toastShow.value = false;
    }, 5000);
  }
});

const inputBlocking = ref(false);
const route = ref("");
const approversText = ref("");
const routeOccupied = ref(false);
const wrongRoute = ref(false);
const wrongApprovers = ref(false);

const isEditing = computed(() => {
  return props.route != null;
});

watch(
  () => route.value,
  () => {
    routeOccupied.value = false;
    wrongRoute.value = false;
  }
);

watch(
  () => approversText.value,
  () => {
    wrongApprovers.value = false;
  }
);

onMounted(() => {
  if (props.route) {
    route.value = props.route.route || "";
    approversText.value = props.route.approvers ? props.route.approvers.join("\n") : "";
  } else {
    route.value = "";
    approversText.value = "";
  }
});

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

function saveRoute() {
  if (route.value.trim() == "") {
    wrongRoute.value = true;
    return;
  }

  const approvers = parseApproverLines(approversText.value);
  if (approvers.length == 0) {
    wrongApprovers.value = true;
    return;
  }

  inputBlocking.value = true;
  axios
    .post("/admin/api/acls/auto-approvers/routes", {
      state: isEditing.value ? "update" : "create",
      route: route.value,
      previousRoute: isEditing.value ? props.route.route : "",
      approvers: approvers,
    })
    .then(function (response) {
      if (response.data["status"] == "success") {
        emit("saved-route");
        emit("close");
      } else if (response.data["status"] == "error-occupied") {
        routeOccupied.value = true;
      } else if (response.data["status"] == "error-自动审批路由无效") {
        wrongRoute.value = true;
      } else if (response.data["status"].startsWith("error-自动审批审批人无效:")) {
        toastMsg.value = "保存自动审批路由失败:" + response.data["status"].substring(6);
        toastShow.value = true;
      } else {
        toastMsg.value = "保存自动审批路由失败:" + response.data["status"].substring(6);
        toastShow.value = true;
      }
    })
    .catch(function (error) {
      toastMsg.value = "保存自动审批路由失败:" + error;
      toastShow.value = true;
    })
    .then(function () {
      inputBlocking.value = false;
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
          {{ isEditing ? "编辑自动审批路由" : "添加自动审批路由" }}
        </div>
      </header>
      <form @submit.prevent="saveRoute">
        <p class="text-gray-700 mb-6">
          为某个 CIDR 前缀设置审批人列表，例如 <code>10.0.0.0/24</code> 或
          <code>fd7a:115c:a1e0::/48</code>。
        </p>

        <label for="auto-approver-route" class="block font-medium mt-6 mb-2">路由前缀</label>
        <input
          v-model="route"
          class="input w-full border focus:outline-blue-500/60 hover:border disabled:hover:border-stone-200 disabled:border-stone-200 border-stone-200 hover:border-stone-400 rounded-md h-9 min-h-fit"
          type="text"
          :disabled="inputBlocking"
          id="auto-approver-route"
          placeholder="例如 10.0.0.0/24"
        />
        <p v-if="routeOccupied" class="text-sm text-red-500 mt-2">该路由前缀已存在</p>
        <p v-if="wrongRoute" class="text-sm text-red-500 mt-2">请输入合法的 CIDR 路由前缀</p>

        <label for="auto-approver-approvers" class="block font-medium mt-6 mb-2">审批人</label>
        <textarea
          v-model="approversText"
          class="textarea w-full border focus:outline-blue-500/60 hover:border disabled:hover:border-stone-200 disabled:border-stone-200 border-stone-200 hover:border-stone-400 rounded-md min-h-[10rem]"
          :disabled="inputBlocking"
          id="auto-approver-approvers"
          placeholder="每行一个审批人，例如&#10;alice&#10;group:netops&#10;tag:router"
        ></textarea>
        <p v-if="wrongApprovers" class="text-sm text-red-500 mt-2">请至少填写一个审批人</p>

        <div class="rounded-md border border-stone-200 bg-stone-50 p-4 mt-6 text-sm text-gray-600">
          <div class="font-medium text-gray-700 mb-2">填写提示</div>
          <ul class="list-disc list-inside space-y-1">
            <li>审批人支持用户、<code>group:xxx</code>、<code>tag:xxx</code> 以及部分 <code>autogroup:*</code> 别名</li>
            <li>路由前缀会在保存时自动规范化，例如 <code>10.0.0.7/24</code> 会变成 <code>10.0.0.0/24</code></li>
          </ul>
        </div>

        <footer class="flex mt-10 justify-end space-x-4">
          <button
            :disabled="inputBlocking"
            @click="$emit('close')"
            class="btn border border-base-300 hover:border-base-300 bg-base-200 hover:bg-base-300 text-black h-9 min-h-fit"
            type="button"
          >
            取消
          </button>
          <button
            :disabled="inputBlocking"
            class="btn border-0 bg-blue-500 hover:bg-blue-900 disabled:bg-blue-500/60 text-white disabled:text-white/60 h-9 min-h-fit"
            type="submit"
          >
            保存
          </button>
        </footer>
      </form>
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

  <Teleport to=".toast-container">
    <Toast :show="toastShow" :msg="toastMsg" @close="toastShow = false"></Toast>
  </Teleport>
</template>

<style scoped></style>
