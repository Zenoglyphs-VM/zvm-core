use sha2::{Digest, Sha256};
use std::{env, fs, io::Read, path::PathBuf};

fn main() {
    println!("cargo:rerun-if-env-changed=ZVM_OP_REVM_SOURCES");
    println!("cargo:rerun-if-changed=upstream-source.lock.json");
    println!("cargo:rerun-if-changed=Cargo.lock");
    let lock: serde_json::Value = serde_json::from_str(include_str!("upstream-source.lock.json"))
        .expect("invalid OP source lock");
    assert_eq!(lock["schema"], 1);
    assert_eq!(
        lock["repository"],
        "https://github.com/ethereum-optimism/optimism"
    );
    assert_eq!(lock["commit"], "e16bffa18420c86dbab693097bc33117f74a5f7d");
    let source = PathBuf::from(
        env::var_os("ZVM_OP_REVM_SOURCES")
            .expect("set ZVM_OP_REVM_SOURCES to the pinned OP source directory"),
    );
    let output = PathBuf::from(env::var_os("OUT_DIR").expect("missing Cargo output directory"));
    let mut modules = String::new();
    for (file, module) in [
        ("spec.rs", "spec"),
        ("precompiles.rs", "precompiles"),
        ("evm.rs", "op_evm"),
    ] {
        let path = source.join(file);
        let metadata = fs::symlink_metadata(&path).expect("missing pinned OP module");
        assert!(
            metadata.is_file()
                && !metadata.file_type().is_symlink()
                && metadata.len() <= 128 * 1024,
            "invalid pinned OP source file"
        );
        let mut raw = Vec::new();
        fs::File::open(&path)
            .expect("cannot open pinned OP module")
            .take(128 * 1024 + 1)
            .read_to_end(&mut raw)
            .expect("cannot read pinned OP module");
        assert!(raw.len() <= 128 * 1024, "OP module exceeds bounds");
        assert_eq!(
            format!("{:x}", Sha256::digest(&raw)),
            lock["files"][file]
                .as_str()
                .expect("missing OP module digest"),
            "OP module digest mismatch"
        );
        let verified = output.join(file);
        fs::write(&verified, raw).expect("cannot materialize verified OP module");
        modules.push_str(&format!(
            "#[path = {:?}] pub mod {};\n",
            verified.to_str().expect("non-UTF8 build output"),
            module
        ));
        println!("cargo:rerun-if-changed={}", path.display());
    }
    modules.push_str("pub use spec::OpSpecId;\npub use op_evm::OpEvm;\n");
    fs::write(output.join("op_modules.rs"), modules)
        .expect("cannot write verified OP module bindings");
    let dependency_lock = fs::read("Cargo.lock").expect("missing runner dependency lock");
    println!(
        "cargo:rustc-env=ZVM_RUNNER_LOCK_SHA256={:x}",
        Sha256::digest(dependency_lock)
    );
    println!(
        "cargo:rustc-env=ZVM_OP_SOURCE_COMMIT={}",
        lock["commit"].as_str().expect("missing OP commit")
    );
}
