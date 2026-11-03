package io.velin.velin_mobile

import android.app.Activity
import android.content.Intent
import io.flutter.embedding.android.FlutterActivity
import io.flutter.embedding.engine.FlutterEngine
import io.flutter.plugin.common.MethodChannel
import java.io.File
import java.util.concurrent.Executors

class MainActivity : FlutterActivity() {
    private val saveRequest = 4105
    private val fileWorker = Executors.newSingleThreadExecutor()
    private var pendingSave: MethodChannel.Result? = null
    private var pendingPath: String? = null

    override fun configureFlutterEngine(flutterEngine: FlutterEngine) {
        super.configureFlutterEngine(flutterEngine)
        MethodChannel(flutterEngine.dartExecutor.binaryMessenger, "velin/tools/files")
            .setMethodCallHandler { call, result ->
                if (call.method != "save") {
                    result.notImplemented()
                    return@setMethodCallHandler
                }
                val path = call.argument<String>("path")
                val name = call.argument<String>("name") ?: "download"
                if (pendingSave != null || path == null || !File(path).isFile) {
                    result.error("save_unavailable", "当前无法保存文件", null)
                    return@setMethodCallHandler
                }
                pendingSave = result
                pendingPath = path
                try {
                    val intent = Intent(Intent.ACTION_CREATE_DOCUMENT).apply {
                        addCategory(Intent.CATEGORY_OPENABLE)
                        type = "application/octet-stream"
                        putExtra(Intent.EXTRA_TITLE, name)
                    }
                    startActivityForResult(intent, saveRequest)
                } catch (e: Exception) {
                    pendingSave = null
                    pendingPath = null
                    result.error("save_unavailable", e.message, null)
                }
            }
    }

    override fun onActivityResult(requestCode: Int, resultCode: Int, data: Intent?) {
        if (requestCode != saveRequest) {
            super.onActivityResult(requestCode, resultCode, data)
            return
        }
        val reply = pendingSave ?: return
        val source = pendingPath
        pendingSave = null
        pendingPath = null
        val destination = data?.data
        if (resultCode != Activity.RESULT_OK || destination == null || source == null) {
            reply.success(false)
            return
        }
        fileWorker.execute {
            try {
                contentResolver.openOutputStream(destination, "w").use { output ->
                    requireNotNull(output) { "无法打开所选保存位置" }
                    File(source).inputStream().use { input -> input.copyTo(output) }
                }
                runOnUiThread { reply.success(true) }
            } catch (e: Exception) {
                runOnUiThread { reply.error("save_failed", e.message, null) }
            }
        }
    }
}
