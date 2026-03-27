<script setup>
import { ref, watch, computed } from "vue";
import { useDisScroll } from "/src/utils.js";

useDisScroll();

const emit = defineEmits(["close", "created", "revoked"]);

const props = defineProps({
  id: String,
  machineName: String,
  shares: {
    type: Array,
    default: () => [],
  },
});

const inputBlocking = ref(false);
const targetIdentity = ref("");
const formError = ref("");

const normalizedShares = computed(() => {
  return (props.shares || []).map((share) => ({
    ...share,
    createdAtText: share.createdAt ? new Date(share.createdAt).toLocaleString() : "",
    acceptedAtText: share.acceptedAt ? new Date(share.acceptedAt).toLocaleString() : "",
    rejectedAtText: share.rejectedAt ? new Date(share.rejectedAt).toLocaleString() : "",
    revokedAtText: share.revokedAt ? new Date(share.revokedAt).toLocaleString() : "",
  }));
});

const canSubmit = computed(() => {
  return !inputBlocking.value && targetIdentity.value.trim() !== "";
});

watch(
  () => props.shares,
  () => {
    formError.value = "";
  }
);

function createShare() {
  const identity = targetIdentity.value.trim();
  if (!identity) {
    formError.value = "请输入目标身份";
    return;
  }

  inputBlocking.value = true;
  formError.value = "";

  axios
    .post("/admin/api/machines", {
      mid: props.id,
      state: "create_share",
      targetIdentity: identity,
    })
    .then(function (response) {
      if (response.data["status"] == "success") {
        targetIdentity.value = "";
        emit("created", response.data["data"]);
      } else {
        formError.value = response.data["status"].substring(6);
      }
    })
    .catch(function (error) {
      formError.value = String(error);
    })
    .finally(function () {
      inputBlocking.value = false;
    });
}

function revokeShare(share) {
  if (!share) {
    return;
  }

  inputBlocking.value = true;
  formError.value = "";

  axios
    .post("/admin/api/machines", {
      state: "revoke_share",
      shareID: share.id,
    })
    .then(function (response) {
      if (response.data["status"] == "success") {
        emit("revoked", share);
      } else {
        formError.value = response.data["status"].substring(6);
      }
    })
    .catch(function (error) {
      formError.value = String(error);
    })
    .finally(function () {
      inputBlocking.value = false;
    });
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

function shareStatusClass(status) {
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

function shareDecisionText(share) {
  if (share.acceptedAtText) {
    return "接受于 " + share.acceptedAtText;
  }
  if (share.rejectedAtText) {
    return "拒绝于 " + share.rejectedAtText;
  }
  if (share.revokedAtText) {
    return "撤销于 " + share.revokedAtText;
  }
  return "";
}

function copyShareLink(share) {
  const shareURL = share?.shareURL;
  if (!shareURL) {
    formError.value = "暂无可复制的邀请链接";
    return;
  }

  navigator.clipboard.writeText(shareURL).then(function () {
    formError.value = "";
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
      class="bg-white rounded-lg relative p-4 md:p-6 text-gray-700 max-w-2xl min-w-[19rem] my-8 mx-auto w-[97%] shadow-2xl"
      style="pointer-events: auto"
    >
      <header class="flex items-center justify-between space-x-4 mb-5 mr-8">
        <div class="font-semibold text-lg truncate">分享设备 {{ machineName }}</div>
      </header>

      <form @submit.prevent="createShare">
        <p class="text-gray-700 mb-4">
          输入目标身份后，将生成一条设备分享邀请链接，对方打开后可明确接受或拒绝。
        </p>
        <label for="share-target-identity" class="block font-medium mb-2">目标身份</label>
        <div class="flex flex-col md:flex-row gap-3 md:items-center">
          <input
            id="share-target-identity"
            v-model="targetIdentity"
            :disabled="inputBlocking"
            class="input w-full z-30 border focus:outline-blue-500/60 hover:border disabled:hover:border-stone-200 disabled:border-stone-200 border-stone-200 hover:border-stone-400 rounded-md h-9 min-h-fit"
            type="text"
            placeholder="例如 user@example.com 或登录名"
          />
          <button
            :disabled="!canSubmit"
            class="btn border-0 bg-blue-500 hover:bg-blue-900 disabled:bg-blue-500/60 text-white disabled:text-white/60 h-9 min-h-fit"
            type="submit"
          >
            创建分享
          </button>
        </div>
        <p v-if="formError" class="text-sm text-red-500 mt-2">{{ formError }}</p>
      </form>

      <section class="mt-8">
        <header class="mb-3">
          <h3 class="font-semibold">分享记录</h3>
          <p class="text-sm text-gray-600">可在这里查看邀请链接、接受结果和撤销状态。</p>
        </header>
        <div
          v-if="normalizedShares.length == 0"
          class="rounded-md border border-stone-200 bg-stone-50 p-6 text-center text-gray-500"
        >
          暂无分享记录
        </div>
        <div v-else class="rounded-md border border-stone-200 divide-y divide-stone-200">
          <div
            v-for="share in normalizedShares"
            :key="share.id"
            class="p-4 flex flex-col gap-3 md:flex-row md:items-center md:justify-between"
          >
            <div class="min-w-0">
              <div class="font-medium break-all">{{ share.targetIdentity }}</div>
              <div class="flex flex-wrap items-center gap-2 text-sm text-gray-600 mt-1">
                <span
                  class="inline-flex items-center align-middle justify-center font-medium border rounded-sm px-2 py-0.5 text-xs"
                  :class="shareStatusClass(share.status)"
                >
                  {{ shareStatusText(share.status) }}
                </span>
                <span v-if="share.createdAtText"> · 创建于 {{ share.createdAtText }}</span>
                <span v-if="shareDecisionText(share)"> · {{ shareDecisionText(share) }}</span>
              </div>
              <div v-if="share.shareURL" class="text-xs text-gray-500 mt-2 break-all">
                链接：{{ share.shareURL }}
              </div>
            </div>
            <div class="flex shrink-0 justify-end gap-2">
              <button
                v-if="share.shareURL"
                @click="copyShareLink(share)"
                class="btn border border-stone-200 bg-white hover:bg-stone-100 text-black h-9 min-h-fit"
                type="button"
              >
                复制链接
              </button>
              <button
                v-if="share.status != 'rejected' && share.status != 'revoked'"
                :disabled="inputBlocking"
                @click="revokeShare(share)"
                class="btn border-0 bg-red-600 hover:bg-red-700 disabled:bg-red-600/60 text-white h-9 min-h-fit"
                type="button"
              >
                撤销
              </button>
            </div>
          </div>
        </div>
      </section>

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

<style scoped></style>
