<script setup>
import { watch, ref, computed, onMounted } from "vue";
import Toast from "../Toast.vue";
import { useDisScroll } from "/src/utils.js";

const emit = defineEmits(["saved-group", "close"]);

const props = defineProps({
  group: Object,
  users: Array,
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
const groupName = ref("");
const selectedUsers = ref([]);
const groupNameOccupied = ref(false);
const wrongGroupName = ref(false);

const isEditing = computed(() => {
  return props.group != null;
});

const sortedUsers = computed(() => {
  const cloned = (props.users || []).slice();
  cloned.sort(function (a, b) {
    return a.loginName.localeCompare(b.loginName);
  });
  return cloned;
});

watch(
  () => groupName.value,
  () => {
    groupNameOccupied.value = false;
    wrongGroupName.value = false;
    groupName.value = groupName.value
      .toLowerCase()
      .replace(/[^-0-9a-z]/gi, "-")
      .replace(/--*/g, "-");
  }
);

onMounted(() => {
  if (props.group) {
    groupName.value = props.group.groupName.substring(6);
    selectedUsers.value = props.group.users ? props.group.users.slice() : [];
  } else {
    groupName.value = "";
    selectedUsers.value = [];
  }
});

function toggleUser(loginName) {
  if (selectedUsers.value.includes(loginName)) {
    selectedUsers.value.splice(selectedUsers.value.indexOf(loginName), 1);
  } else {
    selectedUsers.value.push(loginName);
    selectedUsers.value.sort();
  }
}

function saveGroup() {
  if (!/^([0-9a-z]|-(?!-))+$/.test(groupName.value)) {
    wrongGroupName.value = true;
    return;
  }
  if (/^(-.*|.*-)$/.test(groupName.value)) {
    wrongGroupName.value = true;
    return;
  }

  inputBlocking.value = true;
  axios
    .post("/admin/api/acls/groups", {
      state: isEditing.value ? "update" : "create",
      groupName: groupName.value,
      users: selectedUsers.value,
    })
    .then(function (response) {
      if (response.data["status"] == "success") {
        emit("saved-group");
        emit("close");
      } else if (response.data["status"] == "error-occupied") {
        groupNameOccupied.value = true;
      } else {
        toastMsg.value = "保存用户组失败:" + response.data["status"].substring(6);
        toastShow.value = true;
      }
    })
    .catch(function (error) {
      toastMsg.value = "保存用户组失败:" + error;
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
          {{ isEditing ? "编辑用户组" : "创建用户组" }}
        </div>
      </header>
      <form @submit.prevent="saveGroup">
        <p class="text-gray-700 mb-6">
          用户组会以 <code>group:{{ groupName || "name" }}</code> 的形式保存，可在 ACL 中复用。
        </p>
        <label for="group-name" class="block font-medium mt-6 mb-2">用户组名称</label>
        <div class="flex mb-2">
          <div class="flex items-center px-3 bg-gray-50 text-gray-500 border rounded-l border-r-0 border-gray-300">
            group:
          </div>
          <div class="relative w-full z-30">
            <input
              v-model="groupName"
              class="input w-full z-30 border focus:outline-blue-500/60 hover:border disabled:hover:border-stone-200 disabled:border-stone-200 border-stone-200 hover:border-stone-400 rounded-l-none rounded-r-md h-9 min-h-fit"
              type="text"
              :disabled="inputBlocking || isEditing"
              id="group-name"
            />
          </div>
        </div>

        <p v-if="groupNameOccupied" class="text-sm text-red-500 mb-2">
          用户组名称 “{{ groupName }}” 已存在
        </p>
        <p v-if="wrongGroupName" class="text-sm text-red-500 mb-2">
          用户组名称不能为空，只能是字母数字和连接线组成，且不能以连接线开头结尾
        </p>

        <label class="block font-medium mt-6 mb-2">成员</label>
        <div class="rounded-md border border-stone-200 bg-stone-50 max-h-72 overflow-y-auto">
          <div v-if="sortedUsers.length == 0" class="p-6 text-center text-gray-500">暂无可选成员</div>
          <label
            v-for="user in sortedUsers"
            :key="user.id"
            class="flex items-center justify-between px-4 py-3 border-b last:border-b-0 border-stone-200 hover:bg-white cursor-pointer"
          >
            <div>
              <div class="font-medium text-sm">{{ user.displayName }}</div>
              <div class="text-xs text-gray-500">{{ user.loginName }}</div>
            </div>
            <input
              :checked="selectedUsers.includes(user.loginName)"
              @change="toggleUser(user.loginName)"
              :disabled="inputBlocking"
              type="checkbox"
              class="checkbox checkbox-sm"
            />
          </label>
        </div>
        <p class="text-sm text-gray-500 mt-2">可为空。空用户组会被保留在 ACL 策略中。</p>

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
            @click="saveGroup"
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

<style scoped>
.checkbox {
  border-width: 1px;
  border-color: #d6d3d1;
}
.checkbox:checked {
  border-color: #3e5db3;
  background-color: #3e5db3;
}
</style>
