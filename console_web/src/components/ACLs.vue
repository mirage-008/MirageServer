<script setup>
import { ref, computed, onMounted, watch } from "vue";
import { onBeforeRouteUpdate, useRoute, useRouter } from "vue-router";

import Tags from "./aclpart/Tags.vue";
import Groups from "./aclpart/Groups.vue";
import Hosts from "./aclpart/Hosts.vue";
import Policy from "./aclpart/Policy.vue";
import Rules from "./aclpart/Rules.vue";
import SSH from "./aclpart/SSH.vue";
import AutoApprovers from "./aclpart/AutoApprovers.vue";

const route = useRoute();
const router = useRouter();
const currentACLPart = ref("");
const availableSections = ref([]);

const aclPartContent = {
  policy: Policy,
  tags: Tags,
  groups: Groups,
  hosts: Hosts,
  rules: Rules,
  ssh: SSH,
  "auto-approvers": AutoApprovers,
};

const aclSectionMeta = {
  policy: {
    label: "JSON 策略",
    icon: "code",
  },
  rules: {
    label: "ACL 规则",
    icon: "list",
  },
  ssh: {
    label: "SSH",
    icon: "list",
  },
  "auto-approvers": {
    label: "自动审批",
    icon: "plus",
  },
  tags: {
    label: "标签",
    icon: "tag",
  },
  groups: {
    label: "用户组",
    icon: "users",
  },
  hosts: {
    label: "主机别名",
    icon: "pulse",
  },
};

const visibleSections = computed(() => {
  return availableSections.value.filter(function (section) {
    return aclSectionMeta[section] && aclPartContent[section];
  });
});

const currentComponent = computed(() => {
  return aclPartContent[currentACLPart.value] || null;
});

function changeACLPart(event) {
  router.push("/acls/" + event.target.value);
}

function ensureValidACLPart() {
  if (visibleSections.value.length == 0) {
    currentACLPart.value = "";
    return;
  }

  if (!visibleSections.value.includes(currentACLPart.value)) {
    const fallback = visibleSections.value[0];
    currentACLPart.value = fallback;
    if (route.params.aclpart !== fallback) {
      router.replace("/acls/" + fallback);
    }
  }
}

function loadACLMeta() {
  return axios
    .get("/admin/api/acls/meta")
    .then(function (response) {
      if (response.data["status"] == "success") {
        availableSections.value = response.data["data"]["sections"] || [];
        ensureValidACLPart();
      }
    })
    .catch(function () {
      availableSections.value = ["policy", "rules", "auto-approvers", "tags", "groups", "hosts"];
      ensureValidACLPart();
    });
}

function iconStrokeWidth(section) {
  return currentACLPart.value == section ? "2.5" : "2";
}

function iconClass(section) {
  return {
    "text-blue-600": currentACLPart.value == section,
    "text-gray-700": currentACLPart.value != section,
  };
}

onBeforeRouteUpdate((to) => {
  currentACLPart.value = to.params.aclpart;
});

watch(visibleSections, function () {
  ensureValidACLPart();
});

onMounted(() => {
  currentACLPart.value = route.params.aclpart;
  loadACLMeta();
});
</script>

<template>
  <main class="container mx-auto pb-20 md:pb-24">
    <section class="md:flex md:mt-16">
      <div class="mb-10 md:mr-20 lg:mr-40">
        <div class="hidden md:block">
          <template v-for="section in visibleSections" :key="section">
            <div class="flex flex-row items-center mb-2">
              <svg
                v-if="aclSectionMeta[section].icon == 'code'"
                xmlns="http://www.w3.org/2000/svg"
                width="20"
                height="20"
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                :stroke-width="iconStrokeWidth(section)"
                stroke-linecap="round"
                stroke-linejoin="round"
                :class="iconClass(section)"
              >
                <path d="m8 9-3 3 3 3"></path>
                <path d="m16 9 3 3-3 3"></path>
              </svg>
              <svg
                v-else-if="aclSectionMeta[section].icon == 'tag'"
                viewBox="0 0 1024 1024"
                xmlns="http://www.w3.org/2000/svg"
                width="20"
                height="20"
                fill="currentColor"
                stroke="currentColor"
                :stroke-width="iconStrokeWidth(section)"
                stroke-linecap="round"
                stroke-linejoin="round"
                :class="iconClass(section)"
              >
                <path
                  d="M947.2 422.4l-12.8-212.8c-3.2-64-56-116.8-120-120l-212.8-12.8c-36.8-1.6-72 11.2-97.6 36.8L113.6 502.4C64 553.6 64 633.6 113.6 683.2l225.6 225.6c49.6 49.6 131.2 49.6 180.8 0l388.8-388.8c27.2-25.6 41.6-60.8 38.4-97.6z m-81.6 52.8L475.2 865.6c-25.6 25.6-65.6 25.6-91.2 0L158.4 638.4c-25.6-25.6-25.6-65.6 0-91.2L548.8 158.4c12.8-12.8 30.4-19.2 49.6-19.2l214.4 12.8c32 1.6 57.6 27.2 60.8 60.8l12.8 212.8c-1.6 19.2-8 36.8-20.8 49.6z"
                ></path>
                <path
                  d="M550.4 292.8c-49.6 49.6-49.6 131.2 0 180.8s131.2 49.6 180.8 0 49.6-131.2 0-180.8-131.2-49.6-180.8 0z m134.4 136c-25.6 25.6-65.6 25.6-91.2 0-25.6-25.6-25.6-65.6 0-91.2 25.6-25.6 65.6-25.6 91.2 0 25.6 25.6 25.6 65.6 0 91.2z"
                ></path>
              </svg>
              <svg
                v-else-if="aclSectionMeta[section].icon == 'users'"
                xmlns="http://www.w3.org/2000/svg"
                width="20"
                height="20"
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                :stroke-width="iconStrokeWidth(section)"
                stroke-linecap="round"
                stroke-linejoin="round"
                :class="iconClass(section)"
              >
                <path d="M17 21v-2a4 4 0 0 0-4-4H5a4 4 0 0 0-4 4v2"></path>
                <circle cx="9" cy="7" r="4"></circle>
                <path d="M23 21v-2a4 4 0 0 0-3-3.87"></path>
                <path d="M16 3.13a4 4 0 0 1 0 7.75"></path>
              </svg>
              <svg
                v-else-if="aclSectionMeta[section].icon == 'pulse'"
                xmlns="http://www.w3.org/2000/svg"
                width="20"
                height="20"
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                :stroke-width="iconStrokeWidth(section)"
                stroke-linecap="round"
                stroke-linejoin="round"
                :class="iconClass(section)"
              >
                <path d="M22 12h-4l-3 9L9 3l-3 9H2"></path>
              </svg>
              <svg
                v-else-if="aclSectionMeta[section].icon == 'list'"
                xmlns="http://www.w3.org/2000/svg"
                width="20"
                height="20"
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                :stroke-width="iconStrokeWidth(section)"
                stroke-linecap="round"
                stroke-linejoin="round"
                :class="iconClass(section)"
              >
                <path d="M9 5h11"></path>
                <path d="M9 12h11"></path>
                <path d="M9 19h11"></path>
                <path d="M4 5h.01"></path>
                <path d="M4 12h.01"></path>
                <path d="M4 19h.01"></path>
              </svg>
              <svg
                v-else
                xmlns="http://www.w3.org/2000/svg"
                width="20"
                height="20"
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                :stroke-width="iconStrokeWidth(section)"
                stroke-linecap="round"
                stroke-linejoin="round"
                :class="iconClass(section)"
              >
                <path d="M12 3v18"></path>
                <path d="M3 12h18"></path>
                <path d="m5.6 5.6 12.8 12.8"></path>
                <path d="m18.4 5.6-12.8 12.8"></path>
              </svg>
              <router-link
                class="flex font-medium ml-4"
                :class="iconClass(section)"
                :to="'/acls/' + section"
              >
                {{ aclSectionMeta[section].label }}
              </router-link>
            </div>
          </template>
        </div>
        <div v-if="visibleSections.length > 0" class="select-with-arrow md:hidden mb-4">
          <select
            v-model.lazy="currentACLPart"
            @change="changeACLPart"
            class="select select-bordered w-full text-lg"
          >
            <option v-for="section in visibleSections" :key="section" :value="section">
              {{ aclSectionMeta[section].label }}
            </option>
          </select>
        </div>
      </div>

      <component :is="currentComponent"></component>
    </section>
  </main>
</template>

<style scoped></style>
