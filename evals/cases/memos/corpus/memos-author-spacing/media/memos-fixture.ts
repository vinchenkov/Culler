import { resolve } from "node:path";

const web = resolve(process.argv[2]!);
const fixture = JSON.parse(await Bun.file(process.argv[3]!).text());
const { fromJson, toBinary } = await import(`${web}/node_modules/@bufbuild/protobuf/dist/esm/index.js`);
const responses: Record<string, unknown> = {
  GetCurrentUser: { user: fixture.current_user }, GetUser: fixture.owner,
  ListUsers: { users: [fixture.owner] }, BatchGetUsers: { users: [fixture.owner] },
  GetInstanceProfile: { version: "evaluation-capture", instanceUrl: fixture.origin, admin: fixture.owner, needsSetup: false },
  GetInstanceSetting: { name: "instance/settings/ACCESS", accessSetting: {} },
  BatchGetInstanceSettings: { settings: [
    { name: "instance/settings/GENERAL", generalSetting: {} },
    { name: "instance/settings/MEMO_RELATED", memoRelatedSetting: {} },
  ] },
  ListUserSettings: { settings: [
    { name: `${fixture.current_user.name}/settings/GENERAL`, generalSetting: { locale: "en", theme: fixture.theme } },
    { name: `${fixture.current_user.name}/settings/TAGS`, tagsSetting: {} },
  ] },
  GetUserStats: { attachmentStorageBytes: fixture.storage_bytes },
  ListMemos: { memos: [fixture.memo] }, GetMemo: fixture.memo,
  ListMemoAttachments: { attachments: fixture.memo.attachments },
  ListAttachments: { attachments: fixture.memo.attachments },
  ListIdentityProviders: {}, ListLinkedIdentities: {}, ListMemoViews: {},
  ListMemoComments: {}, ListSpaces: {}, ListUserNotifications: {},
};
const result: Record<string, string> = {};
for await (const name of new Bun.Glob("*_pb.ts").scan(`${web}/src/types/proto/api/v1`)) {
  const module = await import(`${web}/src/types/proto/api/v1/${name}`);
  for (const service of Object.values(module) as any[]) {
    if (service?.kind !== "service") continue;
    for (const method of service.methods) {
      if (!(method.name in responses)) continue;
      const value = method.name === "GetUserStats" && !method.output.fields.some((field: any) => field.jsonName === "attachmentStorageBytes")
        ? {} : responses[method.name];
      const message = fromJson(method.output, value, { ignoreUnknownFields: false });
      result[`/${service.typeName}/${method.name}`] = Buffer.from(toBinary(method.output, message)).toString("base64");
    }
  }
}
console.log(JSON.stringify(result));
