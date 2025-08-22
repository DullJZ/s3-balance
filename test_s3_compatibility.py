#!/usr/bin/env python3
"""
S3 Balance - S3兼容性测试脚本
使用boto3 AWS SDK测试S3 Balance的S3兼容性
"""

import os
import sys
import time
import hashlib
import tempfile
from datetime import datetime

try:
    import boto3
    from botocore.client import Config
except ImportError:
    print("请先安装boto3: pip install boto3")
    sys.exit(1)

# S3 Balance服务配置
S3_BALANCE_ENDPOINT = "http://localhost:8080"
ACCESS_KEY = "AKIAIOSFODNN7EXAMPLE"
SECRET_KEY = "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"

# 测试配置
TEST_BUCKET = "test-bucket-1"
TEST_KEY_PREFIX = f"test-{int(time.time())}"

def create_s3_client():
    """创建S3客户端"""
    return boto3.client(
        's3',
        endpoint_url=S3_BALANCE_ENDPOINT,
        aws_access_key_id=ACCESS_KEY,
        aws_secret_access_key=SECRET_KEY,
        config=Config(
            signature_version='s3v4',
            s3={'addressing_style': 'path'}
        ),
        region_name='us-east-1'
    )

def test_list_buckets(s3_client):
    """测试列出存储桶"""
    print("\n1. 测试列出存储桶 (ListBuckets)...")
    try:
        response = s3_client.list_buckets()
        buckets = response.get('Buckets', [])
        print(f"   ✓ 找到 {len(buckets)} 个存储桶")
        for bucket in buckets:
            print(f"     - {bucket['Name']} (创建时间: {bucket['CreationDate']})")
        return True
    except Exception as e:
        print(f"   ✗ 失败: {e}")
        return False

def test_upload_object(s3_client):
    """测试上传对象"""
    print(f"\n2. 测试上传对象 (PutObject)...")
    
    # 创建测试文件
    test_data = b"Hello, S3 Balance! This is a test file."
    test_key = f"{TEST_KEY_PREFIX}/test-upload.txt"
    
    try:
        # 上传对象
        response = s3_client.put_object(
            Bucket=TEST_BUCKET,
            Key=test_key,
            Body=test_data,
            ContentType='text/plain',
            Metadata={'test': 'true', 'timestamp': str(int(time.time()))}
        )
        
        etag = response.get('ETag', '').strip('"')
        print(f"   ✓ 成功上传对象: {test_key}")
        print(f"     ETag: {etag}")
        return test_key
    except Exception as e:
        print(f"   ✗ 失败: {e}")
        return None

def test_list_objects(s3_client):
    """测试列出对象"""
    print(f"\n3. 测试列出对象 (ListObjects)...")
    
    try:
        response = s3_client.list_objects_v2(
            Bucket=TEST_BUCKET,
            Prefix=TEST_KEY_PREFIX,
            MaxKeys=10
        )
        
        objects = response.get('Contents', [])
        print(f"   ✓ 找到 {len(objects)} 个对象")
        for obj in objects:
            print(f"     - {obj['Key']} (大小: {obj['Size']} bytes, 修改时间: {obj['LastModified']})")
        return True
    except Exception as e:
        print(f"   ✗ 失败: {e}")
        return False

def test_download_object(s3_client, key):
    """测试下载对象"""
    print(f"\n4. 测试下载对象 (GetObject)...")
    
    if not key:
        print("   ⚠ 跳过: 没有可下载的对象")
        return False
    
    try:
        response = s3_client.get_object(
            Bucket=TEST_BUCKET,
            Key=key
        )
        
        data = response['Body'].read()
        content_type = response.get('ContentType', '')
        content_length = response.get('ContentLength', 0)
        
        print(f"   ✓ 成功下载对象: {key}")
        print(f"     内容类型: {content_type}")
        print(f"     内容长度: {content_length} bytes")
        print(f"     内容预览: {data[:50].decode('utf-8', errors='ignore')}...")
        return True
    except Exception as e:
        print(f"   ✗ 失败: {e}")
        return False

def test_head_object(s3_client, key):
    """测试获取对象元数据"""
    print(f"\n5. 测试获取对象元数据 (HeadObject)...")
    
    if not key:
        print("   ⚠ 跳过: 没有可查询的对象")
        return False
    
    try:
        response = s3_client.head_object(
            Bucket=TEST_BUCKET,
            Key=key
        )
        
        print(f"   ✓ 成功获取对象元数据: {key}")
        print(f"     内容长度: {response.get('ContentLength', 0)} bytes")
        print(f"     内容类型: {response.get('ContentType', '')}")
        print(f"     ETag: {response.get('ETag', '').strip('\"')}")
        print(f"     最后修改: {response.get('LastModified', '')}")
        return True
    except Exception as e:
        print(f"   ✗ 失败: {e}")
        return False

def test_multipart_upload(s3_client):
    """测试分片上传（大文件）"""
    print(f"\n6. 测试分片上传 (Multipart Upload)...")
    
    # 创建一个5MB的测试文件
    test_key = f"{TEST_KEY_PREFIX}/test-multipart.bin"
    part_size = 5 * 1024 * 1024  # 5MB per part
    total_size = 10 * 1024 * 1024  # 10MB total
    
    try:
        # 初始化分片上传
        response = s3_client.create_multipart_upload(
            Bucket=TEST_BUCKET,
            Key=test_key,
            ContentType='application/octet-stream'
        )
        upload_id = response['UploadId']
        print(f"   ✓ 初始化分片上传，UploadId: {upload_id}")
        
        # 上传分片
        parts = []
        for i in range(2):  # 上传2个5MB的分片
            part_number = i + 1
            part_data = os.urandom(part_size)  # 生成随机数据
            
            part_response = s3_client.upload_part(
                Bucket=TEST_BUCKET,
                Key=test_key,
                PartNumber=part_number,
                UploadId=upload_id,
                Body=part_data
            )
            
            parts.append({
                'ETag': part_response['ETag'],
                'PartNumber': part_number
            })
            print(f"   ✓ 上传分片 {part_number}/2 完成")
        
        # 完成分片上传
        s3_client.complete_multipart_upload(
            Bucket=TEST_BUCKET,
            Key=test_key,
            UploadId=upload_id,
            MultipartUpload={'Parts': parts}
        )
        
        print(f"   ✓ 分片上传完成: {test_key}")
        return test_key
    except Exception as e:
        print(f"   ✗ 失败: {e}")
        return None

def test_delete_object(s3_client, key):
    """测试删除对象"""
    print(f"\n7. 测试删除对象 (DeleteObject)...")
    
    if not key:
        print("   ⚠ 跳过: 没有可删除的对象")
        return False
    
    try:
        s3_client.delete_object(
            Bucket=TEST_BUCKET,
            Key=key
        )
        
        print(f"   ✓ 成功删除对象: {key}")
        return True
    except Exception as e:
        print(f"   ✗ 失败: {e}")
        return False

def test_presigned_url(s3_client):
    """测试预签名URL"""
    print(f"\n8. 测试预签名URL...")
    
    test_key = f"{TEST_KEY_PREFIX}/test-presigned.txt"
    
    try:
        # 生成上传预签名URL
        upload_url = s3_client.generate_presigned_url(
            'put_object',
            Params={'Bucket': TEST_BUCKET, 'Key': test_key},
            ExpiresIn=3600
        )
        print(f"   ✓ 生成上传预签名URL")
        print(f"     URL: {upload_url[:80]}...")
        
        # 生成下载预签名URL
        download_url = s3_client.generate_presigned_url(
            'get_object',
            Params={'Bucket': TEST_BUCKET, 'Key': test_key},
            ExpiresIn=3600
        )
        print(f"   ✓ 生成下载预签名URL")
        print(f"     URL: {download_url[:80]}...")
        
        return True
    except Exception as e:
        print(f"   ✗ 失败: {e}")
        return False

def cleanup(s3_client):
    """清理测试数据"""
    print(f"\n清理测试数据...")
    
    try:
        # 列出所有测试对象
        response = s3_client.list_objects_v2(
            Bucket=TEST_BUCKET,
            Prefix=TEST_KEY_PREFIX
        )
        
        objects = response.get('Contents', [])
        if objects:
            # 删除所有测试对象
            delete_objects = [{'Key': obj['Key']} for obj in objects]
            s3_client.delete_objects(
                Bucket=TEST_BUCKET,
                Delete={'Objects': delete_objects}
            )
            print(f"   ✓ 删除了 {len(objects)} 个测试对象")
        else:
            print("   ✓ 没有需要清理的对象")
        
        return True
    except Exception as e:
        print(f"   ✗ 清理失败: {e}")
        return False

def main():
    """主测试函数"""
    print("=" * 60)
    print("S3 Balance - S3兼容性测试")
    print("=" * 60)
    print(f"端点: {S3_BALANCE_ENDPOINT}")
    print(f"测试桶: {TEST_BUCKET}")
    print(f"测试前缀: {TEST_KEY_PREFIX}")
    
    # 创建S3客户端
    s3_client = create_s3_client()
    
    # 执行测试
    results = []
    
    # 基础测试
    results.append(("ListBuckets", test_list_buckets(s3_client)))
    
    # 对象操作测试
    uploaded_key = test_upload_object(s3_client)
    results.append(("PutObject", uploaded_key is not None))
    
    results.append(("ListObjects", test_list_objects(s3_client)))
    results.append(("GetObject", test_download_object(s3_client, uploaded_key)))
    results.append(("HeadObject", test_head_object(s3_client, uploaded_key)))
    
    # 高级功能测试
    # multipart_key = test_multipart_upload(s3_client)
    # results.append(("MultipartUpload", multipart_key is not None))
    
    results.append(("PresignedURL", test_presigned_url(s3_client)))
    
    # 删除测试
    results.append(("DeleteObject", test_delete_object(s3_client, uploaded_key)))
    
    # 清理
    cleanup(s3_client)
    
    # 打印测试结果摘要
    print("\n" + "=" * 60)
    print("测试结果摘要")
    print("=" * 60)
    
    passed = sum(1 for _, result in results if result)
    total = len(results)
    
    for test_name, result in results:
        status = "✓ 通过" if result else "✗ 失败"
        print(f"{test_name:20} {status}")
    
    print("-" * 60)
    print(f"总计: {passed}/{total} 测试通过")
    
    if passed == total:
        print("\n🎉 所有测试通过！S3 Balance S3兼容性良好。")
    else:
        print(f"\n⚠️ 有 {total - passed} 个测试失败，请检查服务配置。")
    
    return 0 if passed == total else 1

if __name__ == "__main__":
    sys.exit(main())
