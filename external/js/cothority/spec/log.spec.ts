import { Logger } from "../src/log";

class ObjectNoString {}

class ObjectWithString {
    toString(): string {
        return "deadbeef";
    }
}

describe("Logger Tests", () => {
    it("should log correctly", () => {
        const buf: string[] = [];
        const logger = new Logger(3);
        logger.out = (str) => buf.push(str);

        logger.lvl1("a", new ObjectNoString());
        expect(buf[0]).toBe("[1] log.spec.ts:17: a [Class ObjectNoString]");

        logger.lvl3("b", new ObjectWithString());
        expect(buf[1]).toBe("[3] log.spec.ts:20: b deadbeef");

        logger.lvl2("c", 1, "text", true, 0xdeadbeef);
        expect(buf[2]).toContain("c 1 text true 3735928559");
    });

    it("should log an error with its stack", () => {
        const error = new Error("deadbeef");
        const buf: string[] = [];
        const logger = new Logger(1);
        logger.out = (str) => buf.push(str);

        logger.error("abc", error);
        expect(buf[0].split("\n").length).toBeGreaterThan(1);
    });
});
