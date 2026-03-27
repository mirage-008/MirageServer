<script setup>
import { computed, onMounted, ref, watch } from "vue";
import Toast from "../Toast.vue";

const toastShow = ref(false);
const toastMsg = ref("");
watch(toastShow, () => {
  if (toastShow.value) {
    setTimeout(function () {
      toastShow.value = false;
    }, 5000);
  }
});

const loading = ref(false);
const saving = ref(false);
const rawPolicy = ref("");
const savedPolicy = ref("");
const sshEnabled = ref(false);
const testsImplemented = ref(false);

const isDirty = computed(() => {
  return rawPolicy.value != savedPolicy.value;
});

function showError(prefix, error) {
  toastMsg.value = prefix + error;
  toastShow.value = true;
}

function loadPolicy() {
  loading.value = true;
  return axios
    .get("/admin/api/acls/policy")
    .then(function (response) {
      if (response.data["status"] == "success") {
        rawPolicy.value = response.data["data"]["raw"] || "";
        savedPolicy.value = rawPolicy.value;
        sshEnabled.value = !!response.data["data"]["sshEnabled"];
        testsImplemented.value = !!response.data["data"]["testsImplemented"];
      } else {
        showError("获取 ACL 策略失败:", response.data["status"].substring(6));
      }
    })
    .catch(function (error) {
      showError("获取 ACL 策略失败:", error);
    })
    .finally(function () {
      loading.value = false;
    });
}

function restoreSaved() {
  rawPolicy.value = savedPolicy.value;
}

function savePolicy() {
  saving.value = true;
  axios
    .post("/admin/api/acls/policy", {
      policy: rawPolicy.value,
    })
    .then(function (response) {
      if (response.data["status"] == "success") {
        rawPolicy.value = response.data["data"]["raw"] || "";
        savedPolicy.value = rawPolicy.value;
        sshEnabled.value = !!response.data["data"]["sshEnabled"];
        testsImplemented.value = !!response.data["data"]["testsImplemented"];
        toastMsg.value = "ACL 策略已保存并格式化";
        toastShow.value = true;
      } else {
        showError("保存 ACL 策略失败:", response.data["status"].substring(6));
      }
    })
    .catch(function (error) {
      showError("保存 ACL 策略失败:", error);
    })
    .finally(function () {
      saving.value = false;
    });
}

onMounted(() => {
  loadPolicy();
});
</script>

<template>
  <div class="flex-1">
    <div class="text-3xl font-semibold tracking-tight leading-tight mb-2 flex items-center">
      <h1 class="mr-2" tabindex="-1">JSON 策略</h1>
    </div>
    <div class="text-gray-600 mt-3 mb-8 space-y-2">
      <p>
        直接编辑组织的整份 <strong>ACL Policy JSON</strong>。保存时会按当前 Mirage ACL 语义做校验，并返回格式化后的标准 JSON。
      </p>
      <p>
        支持带注释和尾逗号的类 JSON 写法；保存后会转换成规范 JSON。现有“ACL 规则 / 标签 / 用户组 / 主机别名 / 自动审批 / SSH”页面编辑的也是同一份策略。
      </p>
      <p v-if="!sshEnabled" class="text-amber-700">
        当前服务端未启用 SSH ACL 功能。JSON 中的 <code>ssh</code> 字段会被保留和校验，但不会生效。
      </p>
      <p v-if="!testsImplemented" class="text-gray-500">
        <code>tests</code> 字段会被保留，但服务端暂未执行 ACL tests。
      </p>
    </div>

    <div class="rounded-xl border border-stone-200 bg-white shadow-sm overflow-hidden">
      <div
        class="flex flex-col gap-3 border-b border-stone-200 px-4 py-4 md:flex-row md:items-center md:justify-between"
      >
        <div>
          <div class="font-semibold text-lg">策略源码</div>
          <div class="text-sm text-gray-500">
            建议直接粘贴整份 policy；保存成功后会自动规范化键值和缩进。
          </div>
        </div>
        <div class="flex flex-wrap gap-3">
          <button
            @click="loadPolicy"
            :disabled="loading || saving"
            class="btn border border-stone-300 hover:border-stone-300 disabled:border-stone-300 bg-base-200 hover:bg-base-300 disabled:bg-base-200/60 text-black disabled:text-black/30 h-9 min-h-fit font-normal"
          >
            {{ loading ? "刷新中…" : "重新加载" }}
          </button>
          <button
            @click="restoreSaved"
            :disabled="loading || saving || !isDirty"
            class="btn border border-stone-300 hover:border-stone-300 disabled:border-stone-300 bg-base-200 hover:bg-base-300 disabled:bg-base-200/60 text-black disabled:text-black/30 h-9 min-h-fit font-normal"
          >
            恢复已保存版本
          </button>
          <button
            @click="savePolicy"
            :disabled="loading || saving"
            class="btn border-0 bg-blue-600 hover:bg-blue-700 disabled:bg-blue-300 text-white h-9 min-h-fit font-normal"
          >
            {{ saving ? "保存中…" : "保存并格式化" }}
          </button>
        </div>
      </div>

      <div class="p-4">
        <textarea
          v-model="rawPolicy"
          spellcheck="false"
          class="textarea textarea-bordered w-full h-[34rem] leading-6 font-mono text-sm resize-y"
          placeholder="{&#10;  &quot;acls&quot;: []&#10;}"
        ></textarea>
        <div class="mt-3 text-sm" :class="isDirty ? 'text-amber-700' : 'text-gray-500'">
          {{ isDirty ? "当前有未保存修改" : "当前内容与已保存版本一致" }}
        </div>
      </div>
    </div>
  </div>

  <Teleport to=".toast-container">
    <Toast :show="toastShow" :msg="toastMsg" @close="toastShow = false"></Toast>
  </Teleport>
</template>

<style scoped></style>
